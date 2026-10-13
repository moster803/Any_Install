// Package games knows, for every supported game, what a mod looks like,
// where it has to go and how to tell what is already installed.
package games

import (
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/steam"
)

// Note is a message for the user. The text is looked up in the user
// interface by Code, so the backend stays language independent.
type Note struct {
	Code   string            `json:"code"`
	Level  string            `json:"level"` // "info", "warn" or "error"
	Params map[string]string `json:"params,omitempty"`
}

func note(level, code string, kv ...string) Note {
	n := Note{Code: code, Level: level}
	if len(kv) > 0 {
		n.Params = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			n.Params[kv[i]] = kv[i+1]
		}
	}
	return n
}

// Found is a mod detected inside a download. It only depends on the
// download itself, so it can be cached.
type Found struct {
	Key         string   `json:"key"` // folder or file inside the download ("" = all of it)
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	ModID       string   `json:"modId"`
	Version     string   `json:"version,omitempty"`
	HasCode     bool     `json:"hasCode,omitempty"`
	Loader      string   `json:"loader,omitempty"`    // Minecraft: fabric, quilt, forge, neoforge
	MCVersion   string   `json:"mcVersion,omitempty"` // Minecraft version requirement as written by the mod
	Deps        []string `json:"deps,omitempty"`
	Unsupported string   `json:"unsupported,omitempty"` // reason code
	Folder      string   `json:"-"`                     // suggested folder or file name
	Parts       []string `json:"-"`                     // game specific details for planning
}

// Installed is a mod that is already in the game.
type Installed struct {
	ID        string   `json:"id"`
	ModID     string   `json:"modId"`
	Name      string   `json:"name"`
	Version   string   `json:"version,omitempty"`
	Kind      string   `json:"kind"`
	Enabled   bool     `json:"enabled"`
	Builtin   bool     `json:"builtin,omitempty"`
	CanToggle bool     `json:"canToggle"`
	Loader    string   `json:"loader,omitempty"`
	MCVersion string   `json:"mcVersion,omitempty"`
	MCLabel   string   `json:"mcLabel,omitempty"`
	Notes     []Note   `json:"notes,omitempty"`
	Paths     []string `json:"-"` // everything that belongs to the mod
	Size      int64    `json:"-"`
}

// Location is where a game and its mods live on this computer.
type Location struct {
	Found   bool   `json:"found"`
	Root    string `json:"root"`    // game folder (or .minecraft)
	ModsDir string `json:"modsDir"` // the folder shown to the user
	Via     string `json:"via"`     // "steam", "custom" or "default"
	Notes   []Note `json:"notes,omitempty"`
	// Backup is where backups are kept. It should be on the same drive as
	// the mods so that moving is instant.
	Backup string `json:"-"`
	// Allowed are the folders an install may change.
	Allowed []string `json:"-"`
	// GameDir is the Steam game folder (may differ from Root).
	GameDir string `json:"-"`
}

// Env gives games access to the computer.
type Env struct {
	Steam  *steam.Steam
	Custom map[string]string // game id -> folder chosen by the user
	HTTP   *http.Client
}

// Game is one supported game.
type Game interface {
	ID() string
	Name() string
	AppID() int // Steam app id, 0 if not a Steam game
	Locate(env *Env) Location
	Analyze(src archive.Source, entries []archive.Entry) []Found
	Installed(loc Location, m *install.Manager) []Installed
	Status(loc Location, inst []Installed, f Found, m *install.Manager) (status, installedVersion string, notes []Note)
	Plan(loc Location, inst []Installed, f Found) (install.Plan, error)
	SetEnabled(loc Location, it Installed, on bool) error
	CanLaunch(loc Location) bool
	Launch(loc Location) error
	Processes() []string
	// Tips are shown after an install, for example how to play together.
	Tips(loc Location, installed []Found) []Note
}

var errUnsupported = errors.New("this kind of mod is not supported yet")

// Status values.
const (
	StatusNew         = "new"
	StatusUpdate      = "update"
	StatusInstalled   = "installed"
	StatusOlder       = "older"
	StatusUnsupported = "unsupported"
)

// All returns every supported game in display order.
func All() []Game {
	return []Game{&SevenDays{}, &EldenRing{}, &Minecraft{}}
}

// ByID finds a game.
func ByID(id string) Game {
	for _, g := range All() {
		if g.ID() == id {
			return g
		}
	}
	return nil
}

// Manager returns the backup manager for a location.
func Manager(loc Location) *install.Manager {
	return &install.Manager{Root: loc.Backup, Allowed: loc.Allowed, Keep: 10}
}

// ---------- shared helpers ----------

// steamLocate finds a Steam game, honouring a folder chosen by the user.
func steamLocate(env *Env, id string, appID int) (dir, via string) {
	if p := env.Custom[id]; p != "" {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p, "custom"
		}
	}
	if env.Steam != nil {
		if app, ok := env.Steam.App(appID); ok {
			return app.Dir, "steam"
		}
	}
	return "", ""
}

// names returns a lookup from lower-case base name to entry names.
func byBase(entries []archive.Entry) map[string][]string {
	m := map[string][]string{}
	for _, e := range entries {
		if e.Dir {
			continue
		}
		b := strings.ToLower(e.Base())
		m[b] = append(m[b], e.Name)
	}
	return m
}

func dirOf(name string) string {
	d := path.Dir(name)
	if d == "." {
		return ""
	}
	return d
}

func under(name, prefix string) bool {
	return prefix == "" || name == prefix || strings.HasPrefix(name, prefix+"/")
}

// fileBase returns a download's file name without extension.
func fileBase(p string) string {
	b := filepath.Base(p)
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		return b
	}
	return strings.TrimSuffix(b, filepath.Ext(b))
}

var versionInName = regexp.MustCompile(`(?i)(?:^|[\s_\-.(\[]v?|v)(\d+(?:\.\d+){1,3}[a-z]?)`)

// VersionFromName guesses a version from a file name like
// "Seamless Co-op v1.8.6.zip" or "NeuesHUD_1.3.zip".
func VersionFromName(name string) string {
	m := versionInName.FindAllStringSubmatch(name, -1)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1][1]
}

// SafeFolderName makes a string usable as a folder name on Windows and
// Linux.
func SafeFolderName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case strings.ContainsRune(`<>:"/\|?*`, r), r < 32:
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(strings.TrimSpace(b.String()), ". ")
	if out == "" {
		out = "Mod"
	}
	return out
}

// CompareVersions compares two version strings piece by piece. Numbers
// are compared as numbers. It returns -1, 0 or 1.
func CompareVersions(a, b string) int {
	pa, pb := versionParts(a), versionParts(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y string
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if x == y {
			continue
		}
		nx, ex := strconv.Atoi(x)
		ny, ey := strconv.Atoi(y)
		switch {
		case ex == nil && ey == nil:
			if nx < ny {
				return -1
			}
			return 1
		case x == "":
			// A missing number counts as 0 ("1.21" == "1.21.0" < "1.21.1"),
			// and a release is newer than its pre-release ("1.2" > "1.2-beta").
			if ey == nil {
				if ny == 0 {
					continue
				}
				return -1
			}
			return 1
		case y == "":
			if ex == nil {
				if nx == 0 {
					continue
				}
				return 1
			}
			return -1
		case ex == nil: // number beats text ("1.2.1" > "1.2.beta")
			return 1
		case ey == nil:
			return -1
		default:
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []string {
	v = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(v, "v"), "V")))
	var parts []string
	var cur strings.Builder
	digit := false
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range v {
		switch {
		case r == '.' || r == '-' || r == '_' || r == '+' || r == ' ':
			flush()
		case unicode.IsDigit(r):
			if !digit {
				flush()
			}
			digit = true
			cur.WriteRune(r)
		default:
			if digit {
				flush()
			}
			digit = false
			cur.WriteRune(r)
		}
	}
	flush()
	return parts
}

// versionStatus compares a download with an installed version.
func versionStatus(have, got string) string {
	if have == "" || got == "" {
		if have == got {
			return StatusInstalled
		}
		return StatusUpdate
	}
	switch CompareVersions(got, have) {
	case 1:
		return StatusUpdate
	case -1:
		return StatusOlder
	}
	return StatusInstalled
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func dirSize(p string) int64 {
	var n int64
	_ = filepath.Walk(p, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}
