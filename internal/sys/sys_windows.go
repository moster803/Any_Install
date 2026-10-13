package sys

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func hidden(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return cmd
}

// Open opens a folder in Explorer or a link (also steam://) with its
// registered program.
func Open(target string) error {
	if !strings.Contains(target, "://") {
		return exec.Command("explorer.exe", target).Start()
	}
	return hidden(exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", target)).Start()
}

// Start runs a program in its own folder.
func Start(exe string) error {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// Running reports whether a process with one of the given names (for
// example "eldenring.exe") is running.
func Running(names []string) bool {
	if len(names) == 0 {
		return false
	}
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if want[strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))] {
			return true
		}
	}
	return false
}

// PickFolder shows the Windows folder picker.
func PickFolder(title string) (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms;` +
		`$f = New-Object System.Windows.Forms.Form -Property @{TopMost=$true};` +
		`$d = New-Object System.Windows.Forms.FolderBrowserDialog;` +
		`$d.Description = '` + strings.ReplaceAll(title, "'", "''") + `';` +
		`$d.UseDescriptionForTitle = $true;` +
		`if ($d.ShowDialog($f) -eq 'OK') { [Console]::OutputEncoding = [Text.Encoding]::UTF8; $d.SelectedPath }`
	out, err := hidden(exec.Command("powershell.exe", "-NoProfile", "-STA", "-Command", script)).Output()
	if err != nil {
		return "", ErrNoPicker
	}
	p := strings.TrimSpace(string(out))
	if p == "" {
		return "", ErrCancelled
	}
	return p, nil
}

// ScreenSize returns the size of the main screen in scaled pixels (the
// unit browsers use for window sizes), so 1920x1080 at 150% is 1280x720.
func ScreenSize() (int, int) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	gsm := user32.NewProc("GetSystemMetrics")
	w, _, _ := gsm.Call(0) // SM_CXSCREEN
	h, _, _ := gsm.Call(1) // SM_CYSCREEN
	// Our manifest declares DPI awareness, so the values above are real
	// pixels. GetDpiForSystem exists from Windows 10 on.
	if p := user32.NewProc("GetDpiForSystem"); p.Find() == nil {
		if dpi, _, _ := p.Call(); dpi > 0 {
			return int(w) * 96 / int(dpi), int(h) * 96 / int(dpi)
		}
	}
	return int(w), int(h)
}

// MessageBox shows a simple error window. It is used when the program
// cannot open its main window at all.
func MessageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, windows.MB_OK|windows.MB_ICONERROR)
}
