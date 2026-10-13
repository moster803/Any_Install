//go:build !windows

package steam

import (
	"os"
	"path/filepath"
)

func osRoots() []string {
	home, _ := os.UserHomeDir()
	return []string{
		filepath.Join(home, ".local", "share", "Steam"),
		filepath.Join(home, ".steam", "steam"),
		filepath.Join(home, ".steam", "root"),
		filepath.Join(home, ".var", "app", "com.valvesoftware.Steam", ".local", "share", "Steam"),
		filepath.Join(home, "snap", "steam", "common", ".local", "share", "Steam"),
	}
}
