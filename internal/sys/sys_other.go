//go:build !windows

package sys

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Open opens a folder or link with the desktop's default program.
func Open(target string) error {
	return exec.Command("xdg-open", target).Start()
}

// Start runs a program in its own folder.
func Start(exe string) error {
	cmd := exec.Command(exe)
	cmd.Dir = filepath.Dir(exe)
	return cmd.Start()
}

// Running reports whether a process with one of the given names is
// running. Names ending in ".exe" also match games running through
// Proton/Wine.
func Running(names []string) bool {
	want := map[string]bool{}
	for _, n := range names {
		want[strings.ToLower(n)] = true
	}
	procs, _ := filepath.Glob("/proc/[0-9]*")
	for _, p := range procs {
		if comm, err := os.ReadFile(filepath.Join(p, "comm")); err == nil {
			if want[strings.ToLower(strings.TrimSpace(string(comm)))] {
				return true
			}
		}
		if cmd, err := os.ReadFile(filepath.Join(p, "cmdline")); err == nil {
			first := strings.SplitN(string(cmd), "\x00", 2)[0]
			base := strings.ToLower(filepath.Base(strings.ReplaceAll(first, `\`, "/")))
			if want[base] {
				return true
			}
		}
	}
	return false
}

// PickFolder uses zenity or kdialog when available.
func PickFolder(title string) (string, error) {
	var cmd *exec.Cmd
	if p, err := exec.LookPath("zenity"); err == nil {
		cmd = exec.Command(p, "--file-selection", "--directory", "--title="+title)
	} else if p, err := exec.LookPath("kdialog"); err == nil {
		cmd = exec.Command(p, "--getexistingdirectory", os.Getenv("HOME"), "--title", title)
	} else {
		return "", ErrNoPicker
	}
	out, err := cmd.Output()
	p := strings.TrimSpace(string(out))
	if err != nil || p == "" {
		return "", ErrCancelled
	}
	return p, nil
}

// ScreenSize is unknown on Linux; the window manager decides.
func ScreenSize() (int, int) { return 0, 0 }

// MessageBox prints to stderr on Linux.
func MessageBox(title, text string) {
	os.Stderr.WriteString(title + ": " + text + "\n")
}
