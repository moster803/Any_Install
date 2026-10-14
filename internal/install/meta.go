package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// metaPath is where Version and SetVersion keep the versions of mods whose
// files carry no version information (for example Seamless Co-op).
func (m *Manager) metaPath() string { return filepath.Join(m.Root, "versions.json") }

func (m *Manager) versions() map[string]string {
	out := map[string]string{}
	if b, err := os.ReadFile(m.metaPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Version returns the remembered version of a mod, or "".
func (m *Manager) Version(modID string) string {
	return m.versions()[strings.ToLower(modID)]
}

// SetVersion remembers the version of a mod.
func (m *Manager) SetVersion(modID, version string) error {
	v := m.versions()
	if version == "" {
		delete(v, strings.ToLower(modID))
	} else {
		v[strings.ToLower(modID)] = version
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(m.metaPath(), b, 0o644)
}

// TrackedMod is a mod whose files are spread over the game folder (for
// example a Cyberpunk 2077 mod with files in archive/ and r6/). Its file
// list is kept so it can be shown, switched off and removed as one.
type TrackedMod struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version,omitempty"`
	Files   []string `json:"files"`
}

func (m *Manager) trackPath() string { return filepath.Join(m.Root, "tracked.json") }

// Tracked returns the remembered mods.
func (m *Manager) Tracked() []TrackedMod {
	var out []TrackedMod
	if b, err := os.ReadFile(m.trackPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

// Track remembers the files of a mod, replacing an earlier entry with the
// same id. An update keeps files it no longer has in the list, so they
// are still removed with the mod.
func (m *Manager) Track(mod TrackedMod) error {
	list := m.Tracked()
	replaced := false
	for i := range list {
		if strings.EqualFold(list[i].ID, mod.ID) {
			seen := map[string]bool{}
			for _, f := range mod.Files {
				seen[f] = true
			}
			for _, f := range list[i].Files {
				if !seen[f] {
					mod.Files = append(mod.Files, f)
				}
			}
			list[i], replaced = mod, true
		}
	}
	if !replaced {
		list = append(list, mod)
	}
	if err := os.MkdirAll(m.Root, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	return os.WriteFile(m.trackPath(), b, 0o644)
}
