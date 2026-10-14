// Package paths finds the folders the program works with: its own data
// folder (portable, next to the program file when possible) and the
// user's download folder.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// DataFolderName is the folder created next to the program file.
const DataFolderName = "AnyInstall-Daten"

// Home returns the user's home folder.
func Home() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// ExeDir returns the folder that contains the running program.
func ExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	// Inside an AppImage the program runs from a read-only mount; the
	// AppImage file itself is where the user expects the data.
	if ai := os.Getenv("APPIMAGE"); ai != "" {
		return filepath.Dir(ai)
	}
	return filepath.Dir(exe)
}

// DataDir returns the folder for settings, imports and logs. It prefers a
// folder next to the program (portable) and falls back to the user's
// config folder if that is not writable.
func DataDir() string {
	if d := os.Getenv("ANYINSTALL_DATA"); d != "" {
		_ = os.MkdirAll(d, 0o755)
		return d
	}
	portable := filepath.Join(ExeDir(), DataFolderName)
	if writable(portable) {
		return portable
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = Home()
	}
	d := filepath.Join(base, "AnyInstall")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func writable(dir string) bool {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

// DownloadsDir returns the user's download folder.
func DownloadsDir() string {
	if d := os.Getenv("ANYINSTALL_DOWNLOADS"); d != "" {
		return d
	}
	if d := downloadsDirOS(); d != "" {
		return d
	}
	return filepath.Join(Home(), "Downloads")
}

// xdgDownloads reads XDG_DOWNLOAD_DIR from ~/.config/user-dirs.dirs, which
// is where Linux desktops store the (possibly translated) folder name.
func xdgDownloads() string {
	if d := os.Getenv("XDG_DOWNLOAD_DIR"); d != "" {
		return d
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(Home(), ".config")
	}
	b, err := os.ReadFile(filepath.Join(cfg, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_DOWNLOAD_DIR=") {
			continue
		}
		v := strings.Trim(strings.TrimPrefix(line, "XDG_DOWNLOAD_DIR="), `"`)
		v = strings.ReplaceAll(v, "$HOME", Home())
		if v != "" && v != Home() {
			return v
		}
	}
	return ""
}

// DocumentsDir returns the user's documents folder, where some games keep
// their mods. On Windows it follows a folder moved by OneDrive.
func DocumentsDir() string {
	if d := os.Getenv("ANYINSTALL_DOCUMENTS"); d != "" {
		return d
	}
	if d := documentsDirOS(); d != "" {
		return d
	}
	return filepath.Join(Home(), "Documents")
}
