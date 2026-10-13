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
