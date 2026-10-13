// Package window opens the user interface in an app window. On Windows it
// uses Microsoft Edge (part of Windows) in app mode, which looks like a
// normal program window and supports touch screens such as the ROG Ally.
// Chrome, Chromium or Brave work as well; the default browser is the last
// resort.
package window

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/moster803/Any_Install/internal/sys"
)

// Open shows url. It returns the started browser process if it could start
// one in app mode, or nil if the default browser was used.
func Open(url string) (*exec.Cmd, error) {
	if os.Getenv("ANYINSTALL_NO_BROWSER") != "" {
		return nil, nil
	}
	for _, exe := range candidates() {
		if exe == "" {
			continue
		}
		if _, err := os.Stat(exe); err != nil {
			if p, err := exec.LookPath(exe); err == nil {
				exe = p
			} else {
				continue
			}
		}
		cmd := exec.Command(exe, args(url)...)
		if err := cmd.Start(); err == nil {
			return cmd, nil
		}
	}
	return nil, sys.Open(url)
}

func profileDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, "AnyInstall", "window")
}

func args(url string) []string {
	a := []string{
		"--app=" + url,
		"--user-data-dir=" + profileDir(),
		"--no-first-run",
		"--no-default-browser-check",
		"--disable-features=Translate,msEdgeSidebarV2,msHubApps,msWebAppsSideloadedAppDefaultProfile",
		"--disable-sync",
	}
	w, h := sys.ScreenSize()
	switch {
	case w > 0 && (w < 1200 || h < 760):
		// Small screens and handhelds like the ROG Ally: use all of it.
		a = append(a, "--start-maximized")
	case w > 0:
		ww, wh := min(1120, w-80), min(800, h-110) // leave room for the taskbar
		a = append(a, "--window-size="+strconv.Itoa(ww)+","+strconv.Itoa(wh),
			"--window-position="+strconv.Itoa((w-ww)/2)+","+strconv.Itoa((h-wh-40)/2))
	default:
		a = append(a, "--window-size=1120,780")
	}
	return a
}

func candidates() []string {
	if runtime.GOOS == "windows" {
		var out []string
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			base := os.Getenv(env)
			if base == "" {
				continue
			}
			out = append(out,
				filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
				filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
				filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			)
		}
		return out
	}
	return []string{
		os.Getenv("ANYINSTALL_BROWSER"),
		"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
		"microsoft-edge", "microsoft-edge-stable", "brave-browser", "brave",
	}
}
