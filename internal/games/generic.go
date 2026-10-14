package games

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/steam"
	"github.com/moster803/Any_Install/internal/sys"
)

// Generic is any Steam game with a mod system Any Install recognises
// (see inspect), or one of the games in the profiles table. It does not
// know which downloads belong to the game, so the user ticks them.
type Generic struct {
	appID int
	app   steam.App
	lay   layout
	prof  *profile
	// lists are the folders whose content "Meine Mods" shows. Locate
	// fills them.
	lists []modList
}

// modList is a folder with one mod per entry, and the folder switched off
// mods are moved to. It must lie outside of everything the game loads.
type modList struct {
	dir, disabled string
	// describe reads name, id and version of an entry. Without it, the
	// entry's name is used, and pak files are grouped.
	describe func(path string, isDir bool) (entryInfo, bool)
	// files are the extensions that count as mods ("" = any). Other
	// files (readme files and the like) are not listed.
	files []string
}

type entryInfo struct {
	id, name, version string
	builtin           bool
}

const genericPrefix = "steam-"

// NewGeneric returns the game with the given Steam app id.
func NewGeneric(appID int) *Generic {
	return &Generic{appID: appID, prof: profileFor(appID)}
}

func (g *Generic) ID() string { return genericPrefix + strconv.Itoa(g.appID) }
func (g *Generic) AppID() int { return g.appID }

func (g *Generic) Name() string {
	if g.app.Name != "" {
		return g.app.Name
	}
	if g.prof != nil {
		return g.prof.name
	}
	return "Steam " + strconv.Itoa(g.appID)
}

func (g *Generic) Processes() []string { return g.lay.exes }

// specialAppIDs are the games with their own, more knowing handler.
var specialAppIDs = map[int]bool{251570: true, 1245620: true}

// Detect returns the games with their own handler followed by every
// installed Steam game where mods can be installed, and those Any Install
// knows it must not touch (Location.Refused).
func Detect(env *Env) []Game {
	out := All()
	if env.Steam == nil {
		return out
	}
	for _, app := range env.Steam.Apps() {
		if specialAppIDs[app.AppID] || steamTool(app.Name) {
			continue
		}
		g := NewGeneric(app.AppID)
		g.app = app
		if l := g.Locate(env); l.Found || l.Refused != "" {
			out = append(out, g)
		}
	}
	return out
}

// steamTool filters Proton, the Steam Linux Runtime and the like.
func steamTool(name string) bool {
	l := strings.ToLower(name)
	for _, p := range []string{"proton", "steam linux runtime", "steamworks", "steamvr"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func (g *Generic) Locate(env *Env) Location {
	if g.app.Dir == "" {
		if env.Steam == nil {
			return Location{}
		}
		app, ok := env.Steam.App(g.appID)
		if !ok {
			return Location{}
		}
		g.app = app
	}
	g.lay = inspect(g.app.Dir)
	g.lists = nil
	if g.prof != nil {
		if g.prof.refuse != "" {
			return Location{Refused: g.prof.refuse, Root: g.app.Dir}
		}
		return g.prof.locate(env, g)
	}
	l := g.lay
	if l.system == "" || (l.system == SysUnreal && (l.antiCheat || l.signed)) {
		// No known mod system; or an Unreal game with anti-cheat, which
		// may ban players for modified files, or with signed paks, which
		// ignores mods.
		return Location{}
	}
	loc := g.baseLocation(l.system, l.modsDir)
	g.lists = []modList{{dir: l.modsDir, disabled: l.disabled, files: systemFiles[l.system]}}
	loc.Notes = append(loc.Notes, note("info", "hint_pick"))
	if l.antiCheat {
		loc.Notes = append(loc.Notes, note("warn", "anticheat"))
	}
	if l.loader && l.proton() {
		loc.Notes = append(loc.Notes, protonOverride(l.system))
	}
	return loc
}

// systemFiles are the files that are mods in the mods folder of each
// system. Folders always count.
var systemFiles = map[string][]string{
	SysBepInEx: {".dll"},
	SysMelon:   {".dll"},
	SysUnreal:  {".pak", ".utoc", ".ucas", ".sig"},
}

// baseLocation is a location whose mods live in the game folder.
func (g *Generic) baseLocation(system, modsDir string) Location {
	return Location{
		Found:   true,
		Root:    g.app.Dir,
		ModsDir: modsDir,
		Via:     "steam",
		Proton:  g.lay.proton(),
		System:  system,
		GameDir: g.app.Dir,
		Backup:  filepath.Join(g.app.Dir, "AnyInstall-Backup"),
		Allowed: []string{g.app.Dir},
	}
}

// protonOverride tells how to make Proton load the mod loader: Wine
// prefers its own copy of the DLL the loader hooks into.
func protonOverride(system string) Note {
	dll := "winhttp"
	switch system {
	case SysMelon:
		dll = "version"
	case "cyberpunk":
		dll = "winmm,version"
	}
	return note("info", "proton_override", "option", `WINEDLLOVERRIDES="`+dll+`=n,b" %command%`)
}

// ---------- installed mods ----------

func (g *Generic) Installed(loc Location, m *install.Manager) []Installed {
	var out []Installed
	taken := map[string]bool{}
	if g.prof != nil && g.prof.tracked {
		for _, it := range trackedInstalled(loc, m) {
			for _, p := range it.Paths {
				taken[p] = true
			}
			out = append(out, it)
		}
	}
	for i, l := range g.lists {
		for _, on := range []bool{true, false} {
			if !on && l.disabled == "" {
				continue
			}
			for _, it := range l.read(i, on, m) {
				if !taken[it.Paths[0]] {
					out = append(out, it)
				}
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// read lists the mods in the folder or, with enabled false, the switched
// off ones. IDs are "<list>:on:<name>" or "<list>:off:<name>".
func (l modList) read(index int, enabled bool, m *install.Manager) []Installed {
	dir := l.dir
	state := "on"
	if !enabled {
		dir, state = l.disabled, "off"
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Installed
	group := map[string]int{} // pak files with the same name are one mod
	for _, e := range ents {
		name := e.Name()
		lower := strings.ToLower(name)
		if strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		ext := strings.ToLower(filepath.Ext(name))
		if !e.IsDir() && (mediaExts[ext] || (len(l.files) > 0 && !hasExt(l.files, ext))) {
			continue
		}
		it := Installed{
			ID: fmt.Sprintf("%d:%s:%s", index, state, name), Kind: "mod", Enabled: enabled, CanToggle: true,
			Name: name, ModID: lower, Paths: []string{full},
		}
		if l.describe != nil {
			info, ok := l.describe(full, e.IsDir())
			if !ok {
				continue
			}
			if info.name != "" {
				it.Name = info.name
			}
			if info.id != "" {
				it.ModID = strings.ToLower(info.id)
			}
			it.Version = info.version
			if info.builtin {
				it.Builtin, it.CanToggle = true, false
			}
		} else if !e.IsDir() {
			stem := strings.TrimSuffix(name, filepath.Ext(name))
			if isPakPart(ext) {
				key := strings.ToLower(stem)
				if i, ok := group[key]; ok {
					out[i].Paths = append(out[i].Paths, full)
					continue
				}
				group[key] = len(out)
			}
			it.Name, it.ModID = stem, strings.ToLower(stem)
			it.ID = fmt.Sprintf("%d:%s:%s", index, state, stem)
		}
		if it.Version == "" {
			it.Version = m.Version(it.ModID)
		}
		out = append(out, it)
	}
	return out
}

func hasExt(list []string, ext string) bool {
	for _, x := range list {
		if x == ext {
			return true
		}
	}
	return false
}

func isPakPart(ext string) bool {
	return ext == ".pak" || ext == ".utoc" || ext == ".ucas" || ext == ".sig"
}

func (g *Generic) SetEnabled(loc Location, it Installed, on bool) error {
	if id, ok := strings.CutPrefix(it.ID, "t:"); ok {
		return setTracked(loc, id, on)
	}
	n, _, ok := strings.Cut(it.ID, ":")
	i, err := strconv.Atoi(n)
	if !ok || err != nil || i < 0 || i >= len(g.lists) || g.lists[i].disabled == "" {
		return errUnsupported
	}
	to := g.lists[i].disabled
	if on {
		to = g.lists[i].dir
	}
	for _, p := range it.Paths {
		target := filepath.Join(to, filepath.Base(p))
		if target == p {
			continue
		}
		if err := install.Move(p, target); err != nil {
			return err
		}
	}
	return nil
}

func (g *Generic) CanLaunch(Location) bool { return true }

func (g *Generic) Launch(Location) error {
	return sys.Open("steam://rungameid/" + strconv.Itoa(g.appID))
}

func (g *Generic) Tips(loc Location, installed []Found) []Note {
	var tips []Note
	for _, f := range installed {
		if f.Kind == "loader" && loc.Proton {
			tips = append(tips, protonOverride(loc.System))
			break
		}
	}
	if g.prof != nil && g.prof.tip != "" {
		tips = append(tips, note("info", g.prof.tip))
	}
	return tips
}

// gameDirEntry returns the entry of dir with the given name in any
// spelling, or "".
func gameDirEntry(dir, name string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if strings.EqualFold(e.Name(), name) {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// ---------- mods with files all over the game folder ----------

// disabledPath is where a file of a switched off tracked mod is kept: in
// AnyInstall-Disabled, with the same path below the game folder.
func disabledPath(loc Location, id, file string) string {
	rel, err := filepath.Rel(loc.GameDir, file)
	if err != nil {
		rel = filepath.Base(file)
	}
	return filepath.Join(loc.GameDir, "AnyInstall-Disabled", SafeFolderName(id), rel)
}

func trackedInstalled(loc Location, m *install.Manager) []Installed {
	var out []Installed
	for _, t := range m.Tracked() {
		var on, off []string
		for _, f := range t.Files {
			if exists(f) {
				on = append(on, f)
			} else if d := disabledPath(loc, t.ID, f); exists(d) {
				off = append(off, d)
			}
		}
		if len(on)+len(off) == 0 {
			continue // removed or undone
		}
		out = append(out, Installed{
			ID: "t:" + t.ID, ModID: strings.ToLower(t.ID), Name: t.Name, Version: t.Version, Kind: "mod",
			Enabled: len(on) > 0, CanToggle: true, Paths: append(on, off...),
		})
	}
	return out
}

func setTracked(loc Location, id string, on bool) error {
	for _, t := range Manager(loc).Tracked() {
		if !strings.EqualFold(t.ID, id) {
			continue
		}
		for _, f := range t.Files {
			d := disabledPath(loc, t.ID, f)
			from, to := f, d
			if on {
				from, to = d, f
			}
			if !exists(from) {
				continue
			}
			if err := install.Move(from, to); err != nil {
				return err
			}
		}
		return nil
	}
	return errUnsupported
}

// Track remembers which files an install put where, for games whose mods
// are spread over the game folder.
func (g *Generic) Track(m *install.Manager, f Found, plan install.Plan) error {
	if g.prof == nil || !g.prof.tracked {
		return nil
	}
	var files []string
	for _, op := range plan.Ops {
		if op.Kind == install.PutFile {
			files = append(files, op.Dst)
		}
	}
	return m.Track(install.TrackedMod{ID: f.ModID, Name: f.Name, Version: f.Version, Files: files})
}
