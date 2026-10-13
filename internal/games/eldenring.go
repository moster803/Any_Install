package games

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/sys"
)

// EldenRing supports the mods two friends need to play together:
//   - Seamless Co-op (SeamlessCoop folder + ersc_launcher.exe in Game)
//   - Elden Mod Loader (dinput8.dll in Game) and DLL mods for it (Game/mods)
//
// Mods for Mod Engine 2 (regulation.bin, parts, chr ...) are recognised
// but not installed yet.
type EldenRing struct{}

func (*EldenRing) ID() string          { return "eldenring" }
func (*EldenRing) Name() string        { return "Elden Ring" }
func (*EldenRing) AppID() int          { return 1245620 }
func (*EldenRing) Processes() []string { return []string{"eldenring.exe"} }

const (
	erCoopID   = "seamlesscoop"
	erLoaderID = "eldenmodloader"
)

func (g *EldenRing) Locate(env *Env) Location {
	dir, via := steamLocate(env, g.ID(), g.AppID())
	if dir == "" {
		return Location{}
	}
	game := filepath.Join(dir, "Game")
	if !isDir(game) && exists(filepath.Join(dir, "eldenring.exe")) {
		// The user picked the Game folder itself.
		game = dir
		dir = filepath.Dir(dir)
	}
	if via == "custom" && !exists(filepath.Join(game, "eldenring.exe")) {
		return Location{} // not the Elden Ring folder
	}
	return Location{
		Found:   true,
		Root:    dir,
		ModsDir: game,
		Via:     via,
		GameDir: game,
		Backup:  filepath.Join(dir, "AnyInstall-Backup"),
		Allowed: []string{dir},
	}
}

// Folders that only Mod Engine 2 can load.
var me2Dirs = map[string]bool{
	"parts": true, "chr": true, "menu": true, "msg": true, "map": true, "param": true,
	"sfx": true, "sd": true, "event": true, "action": true, "script": true, "asset": true,
	"font": true, "mtd": true, "obj": true, "material": true, "shader": true,
}

func (g *EldenRing) Analyze(src archive.Source, entries []archive.Entry) []Found {
	bb := byBase(entries)
	base := fileBase(src.Path())

	if ersc := bb["ersc.dll"]; len(ersc) > 0 {
		coopDir := dirOf(ersc[0])
		root := dirOf(coopDir)
		var launcher string
		for _, l := range bb["ersc_launcher.exe"] {
			if launcher == "" || dirOf(l) == root {
				launcher = l
			}
		}
		return []Found{{
			Key: root, Kind: "coop", Name: "Seamless Co-op", ModID: erCoopID,
			Version: VersionFromName(base), HasCode: true,
			Parts: []string{coopDir, launcher},
		}}
	}

	if d8 := bb["dinput8.dll"]; len(d8) > 0 {
		root := dirOf(d8[0])
		hasMods := false
		for _, e := range entries {
			if e.Dir && strings.EqualFold(path.Base(e.Name), "mods") && dirOf(e.Name) == root {
				hasMods = true
			}
		}
		if len(bb["mod_loader_config.ini"]) > 0 || hasMods {
			return []Found{{Key: root, Kind: "loader", Name: "Elden Mod Loader", ModID: erLoaderID, Version: VersionFromName(base), HasCode: true}}
		}
	}

	me2 := len(bb["modengine2_launcher.exe"]) > 0 || len(bb["launchmod_eldenring.bat"]) > 0 ||
		len(bb["config_eldenring.toml"]) > 0 || len(bb["regulation.bin"]) > 0
	if !me2 {
		for _, e := range entries {
			if e.Dir && strings.Count(e.Name, "/") <= 1 && me2Dirs[strings.ToLower(path.Base(e.Name))] {
				me2 = true
				break
			}
		}
	}
	if me2 {
		return []Found{{Kind: "me2", Name: base, ModID: strings.ToLower(base), Unsupported: "needs_modengine"}}
	}

	// DLL mods for the Elden Mod Loader. Many other games use DLLs too, so
	// downloads that clearly belong to another game are skipped.
	if foreignMod(bb, entries) {
		return nil
	}
	var dlls []archive.Entry
	for _, e := range entries {
		if !e.Dir && strings.EqualFold(path.Ext(e.Name), ".dll") {
			dlls = append(dlls, e)
		}
	}
	if len(dlls) == 0 {
		return nil
	}
	key := ""
	if i := indexModsDir(dlls[0].Name); i >= 0 {
		key = dlls[0].Name[:i]
	} else {
		key = dirOf(dlls[0].Name)
		for _, d := range dlls[1:] {
			for !under(d.Name, key) {
				key = dirOf(key)
			}
		}
	}
	var parts []string
	for _, e := range entries {
		if !e.Dir && under(e.Name, key) {
			rel := strings.TrimPrefix(strings.TrimPrefix(e.Name, key), "/")
			parts = append(parts, rel+"|"+strconv.FormatInt(e.Size, 10))
		}
	}
	name := base
	if len(dlls) == 1 {
		name = strings.TrimSuffix(dlls[0].Base(), path.Ext(dlls[0].Name))
	}
	return []Found{{Key: key, Kind: "dllmod", Name: name, ModID: strings.ToLower(name), Version: VersionFromName(base), HasCode: true, Parts: parts}}
}

// foreignMod reports whether a download is clearly a mod for another game
// (7 Days to Die, Minecraft, BepInEx/Unity games).
func foreignMod(bb map[string][]string, entries []archive.Entry) bool {
	for _, marker := range []string{"modinfo.xml", "fabric.mod.json", "quilt.mod.json", "pack.mcmeta", "mods.toml", "manifest.json", "bepinex.cfg"} {
		if len(bb[marker]) > 0 {
			return true
		}
	}
	for _, e := range entries {
		base := strings.ToLower(path.Base(e.Name))
		if strings.HasSuffix(base, ".jar") || (e.Dir && (base == "bepinex" || base == "plugins")) {
			return true
		}
	}
	return false
}

// indexModsDir returns the end of a "mods" path component in name (so that
// name[:i] is ".../mods"), or -1.
func indexModsDir(name string) int {
	parts := strings.Split(name, "/")
	pos := 0
	for _, p := range parts[:len(parts)-1] {
		pos += len(p)
		if strings.EqualFold(p, "mods") {
			return pos
		}
		pos++ // the slash
	}
	return -1
}

func (g *EldenRing) coopIni(loc Location) string {
	return filepath.Join(loc.GameDir, "SeamlessCoop", "ersc_settings.ini")
}

// CoopPassword returns the Seamless Co-op password and whether Seamless
// Co-op is installed.
func (g *EldenRing) CoopPassword(loc Location) (string, bool) {
	b, err := os.ReadFile(g.coopIni(loc))
	if err != nil {
		return "", exists(filepath.Join(loc.GameDir, "SeamlessCoop", "ersc.dll"))
	}
	return install.ParseIni(string(b))["cooppassword"], true
}

// SetCoopPassword writes the Seamless Co-op password.
func (g *EldenRing) SetCoopPassword(loc Location, pw string) error {
	p := g.coopIni(loc)
	b, _ := os.ReadFile(p)
	text := install.SetIniValue(string(b), "PASSWORD", "cooppassword", pw)
	return os.WriteFile(p, []byte(text), 0o644)
}

func (g *EldenRing) Installed(loc Location, m *install.Manager) []Installed {
	var out []Installed
	game := loc.GameDir
	loader := exists(filepath.Join(game, "dinput8.dll"))
	if exists(filepath.Join(game, "SeamlessCoop", "ersc.dll")) {
		it := Installed{
			ID: erCoopID, ModID: erCoopID, Kind: "coop", Name: "Seamless Co-op", Enabled: true,
			Paths: []string{filepath.Join(game, "SeamlessCoop"), filepath.Join(game, "ersc_launcher.exe")},
		}
		if m != nil {
			it.Version = m.Version(erCoopID)
		}
		if pw, _ := g.CoopPassword(loc); pw == "" {
			it.Notes = append(it.Notes, note("warn", "coop_no_password"))
		}
		out = append(out, it)
	}
	if loader {
		it := Installed{
			ID: erLoaderID, ModID: erLoaderID, Kind: "loader", Name: "Elden Mod Loader", Enabled: true,
			Paths: []string{filepath.Join(game, "dinput8.dll"), filepath.Join(game, "mod_loader_config.ini")},
		}
		if m != nil {
			it.Version = m.Version(erLoaderID)
		}
		out = append(out, it)
	}
	for _, d := range []struct {
		dir string
		on  bool
	}{{filepath.Join(game, "mods"), true}, {filepath.Join(game, "mods"+disabledSuffix), false}} {
		ents, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".dll") {
				continue
			}
			stem := strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))
			it := Installed{
				ID: "dll:" + strings.ToLower(e.Name()), ModID: strings.ToLower(stem), Kind: "dllmod",
				Name: stem, Enabled: d.on, CanToggle: true,
			}
			// The dll plus its config file or folder of the same name.
			for _, x := range ents {
				xs := strings.TrimSuffix(x.Name(), filepath.Ext(x.Name()))
				if strings.EqualFold(xs, stem) || strings.EqualFold(x.Name(), stem) {
					it.Paths = append(it.Paths, filepath.Join(d.dir, x.Name()))
				}
			}
			if !loader {
				it.Notes = append(it.Notes, note("warn", "needs_er_loader"))
			}
			out = append(out, it)
		}
	}
	return out
}

func (g *EldenRing) Status(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
	switch f.Kind {
	case "me2":
		return StatusUnsupported, "", []Note{note("warn", "needs_modengine")}
	case "coop", "loader":
		for _, it := range inst {
			if it.ModID == f.ModID {
				return versionStatus(it.Version, f.Version), it.Version, nil
			}
		}
		return StatusNew, "", nil
	case "dllmod":
		var notes []Note
		if !exists(filepath.Join(loc.GameDir, "dinput8.dll")) {
			notes = append(notes, note("warn", "needs_er_loader"))
		}
		same, any := true, false
		for _, p := range f.Parts {
			rel, size, _ := strings.Cut(p, "|")
			if !strings.EqualFold(filepath.Ext(rel), ".dll") {
				continue
			}
			st, err := os.Stat(filepath.Join(loc.GameDir, "mods", filepath.FromSlash(rel)))
			if err != nil {
				same = false
				continue
			}
			any = true
			if strconv.FormatInt(st.Size(), 10) != size {
				same = false
			}
		}
		switch {
		case any && same:
			return StatusInstalled, "", notes
		case any:
			return StatusUpdate, "", notes
		}
		return StatusNew, "", notes
	}
	return StatusUnsupported, "", nil
}

func (g *EldenRing) Plan(loc Location, inst []Installed, f Found) (install.Plan, error) {
	game := loc.GameDir
	var p install.Plan
	switch f.Kind {
	case "coop":
		coopDir, launcher := f.Parts[0], f.Parts[1]
		p.Ops = append(p.Ops, install.Op{Kind: install.ReplaceDir, Src: coopDir, Dst: filepath.Join(game, "SeamlessCoop")})
		if launcher != "" {
			p.Ops = append(p.Ops, install.Op{Kind: install.PutFile, Src: launcher, Dst: filepath.Join(game, "ersc_launcher.exe")})
		}
		// Launcher name used by old versions.
		if old := filepath.Join(game, "launch_elden_ring_seamlesscoop.exe"); exists(old) {
			p.Ops = append(p.Ops, install.Op{Kind: install.Remove, Dst: old})
		}
		p.KeepIni = []string{g.coopIni(loc)}
	case "loader":
		p.Ops = append(p.Ops, install.Op{Kind: install.MergeDir, Src: f.Key, Dst: game})
	case "dllmod":
		p.Ops = append(p.Ops, install.Op{Kind: install.MergeDir, Src: f.Key, Dst: filepath.Join(game, "mods")})
	default:
		return p, errUnsupported
	}
	return p, nil
}

func (g *EldenRing) SetEnabled(loc Location, it Installed, on bool) error {
	from := filepath.Join(loc.GameDir, "mods")
	to := from + disabledSuffix
	if on {
		from, to = to, from
	}
	sort.Strings(it.Paths)
	for _, p := range it.Paths {
		if filepath.Dir(p) != from {
			continue
		}
		if err := install.Move(p, filepath.Join(to, filepath.Base(p))); err != nil {
			return err
		}
	}
	return nil
}

func (g *EldenRing) launcher(loc Location) string {
	return filepath.Join(loc.GameDir, "ersc_launcher.exe")
}

func (g *EldenRing) CanLaunch(Location) bool { return true }

// Launch starts the game through the Seamless Co-op launcher when it is
// installed (on Windows), otherwise through Steam.
func (g *EldenRing) Launch(loc Location) error {
	if runtime.GOOS == "windows" && exists(g.launcher(loc)) {
		return sys.Start(g.launcher(loc))
	}
	return sys.Open("steam://rungameid/1245620")
}

func (g *EldenRing) Tips(loc Location, installed []Found) []Note {
	var tips []Note
	coop, dll := false, false
	for _, f := range installed {
		coop = coop || f.Kind == "coop"
		dll = dll || f.Kind == "dllmod"
	}
	if coop {
		tips = append(tips, note("info", "tip_er_coop"))
	}
	if dll && !coop {
		tips = append(tips, note("warn", "tip_er_offline"))
	}
	return tips
}
