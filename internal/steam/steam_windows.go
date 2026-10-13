package steam

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

func osRoots() []string {
	var out []string
	if k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Valve\Steam`, registry.QUERY_VALUE); err == nil {
		if v, _, err := k.GetStringValue("SteamPath"); err == nil {
			out = append(out, filepath.Clean(v))
		}
		k.Close()
	}
	for _, key := range []string{`SOFTWARE\WOW6432Node\Valve\Steam`, `SOFTWARE\Valve\Steam`} {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, key, registry.QUERY_VALUE); err == nil {
			if v, _, err := k.GetStringValue("InstallPath"); err == nil {
				out = append(out, filepath.Clean(v))
			}
			k.Close()
		}
	}
	pf := os.Getenv("ProgramFiles(x86)")
	if pf == "" {
		pf = `C:\Program Files (x86)`
	}
	out = append(out, filepath.Join(pf, "Steam"))
	return out
}
