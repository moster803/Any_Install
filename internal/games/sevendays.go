package games

import (
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/sys"
)

// SevenDays is 7 Days to Die. A mod is a folder that contains a
// ModInfo.xml. Since version 1.0 the game prefers the Mods folder in its
// user data folder (%APPDATA%\7DaysToDie\Mods on Windows); the Mods folder
// inside the game folder still works and is read as well.
type SevenDays struct{}

func (*SevenDays) ID() string          { return "7dtd" }
func (*SevenDays) Name() string        { return "7 Days to Die" }
func (*SevenDays) AppID() int          { return 251570 }
func (*SevenDays) Processes() []string { return []string{"7DaysToDie.exe", "7DaysToDie.x86_64"} }

const disabledSuffix = "_disabled"

// ErrStartGameFirst means the game runs through Proton but has never been
// started, so its prefix with the user data folder does not exist yet.
var ErrStartGameFirst = errors.New("start the game once so that Proton creates its folder")

// sevenDaysUserData returns the game's user data folder, whose Mods folder
// the game loads. On Linux the game runs either natively or, when Proton
// is forced in its Steam properties, as the Windows version. Then its
// %APPDATA% is inside the Proton prefix in the game's Steam library.
func sevenDaysUserData(env *Env, gameDir string) (dir string, proton bool, notes []Note) {
	if d := os.Getenv("ANYINSTALL_7DTD_USERDATA"); d != "" {
		return d, false, nil
	}
	if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "7DaysToDie"), false, nil
		}
	}
	native := nativeUserData(env)
	pfx := protonPrefix(env, gameDir, 251570)
	inPfx := filepath.Join(prefixUser(pfx), "AppData", "Roaming", "7DaysToDie")
	var tool string
	if env.Steam != nil {
		// When Steam's settings can't be read, tool stays "" and the
		// folder used last decides below, which is the best guess left.
		tool, _ = env.Steam.CompatTool(251570)
	}
	switch {
	case tool != "" && !strings.HasPrefix(strings.ToLower(tool), "steamlinuxruntime"):
		if !protonReady(pfx) {
			notes = append(notes, note("warn", "proton_no_prefix"))
		}
		return inPfx, true, notes
	case tool != "":
		return native, false, nil
	}
	// Nothing forced by the user. Steam can still pick Proton by itself
	// (on the Steam Deck, for example), so the folder used last wins.
	if isDir(inPfx) && (!isDir(native) || lastUsed(inPfx).After(lastUsed(native))) {
		return inPfx, true, nil
	}
	return native, false, nil
}

// protonReady reports whether Proton has set up a prefix. Proton does that
// at the first start of the game. Folders alone don't count: opening the
// Mods folder creates them.
func protonReady(pfx string) bool {
	return exists(filepath.Join(pfx, "system.reg"))
}

// Ready returns ErrStartGameFirst when nothing may be written to the
// game's mods folder yet.
func Ready(loc Location) error {
	if loc.WaitStart || (loc.Prefix != "" && !protonReady(loc.Prefix)) {
		return ErrStartGameFirst
	}
	return nil
}

// prefixUser is the Windows user folder inside a Proton prefix.
func prefixUser(pfx string) string {
	return filepath.Join(pfx, "drive_c", "users", "steamuser")
}

// nativeUserData is the Linux version's user data folder: the data folder
// of the Flatpak or Snap sandbox when Steam runs in one, else
// $XDG_DATA_HOME or ~/.local/share, each plus 7DaysToDie.
func nativeUserData(env *Env) string {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".local", "share", "7DaysToDie")
	if env.Steam != nil {
		root := filepath.Clean(env.Steam.Root)
		suffix := string(filepath.Separator) + filepath.Join(".local", "share", "Steam")
		if sandbox := strings.TrimSuffix(root, suffix); sandbox != root && sandbox != filepath.Clean(home) {
			return filepath.Join(sandbox, ".local", "share", "7DaysToDie")
		}
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		// A game that ignores the setting has used the default folder.
		if d := filepath.Join(x, "7DaysToDie"); isDir(d) || !isDir(def) {
			return d
		}
	}
	return def
}

// protonPrefix is the Proton prefix of a game. Steam keeps it in the
// library that holds the game.
func protonPrefix(env *Env, gameDir string, appID int) string {
	lib := filepath.Dir(filepath.Dir(filepath.Dir(gameDir))) // <library>/steamapps/common/<game>
	if env.Steam != nil {
		if app, ok := env.Steam.App(appID); ok {
			lib = app.Library
		}
	}
	return filepath.Join(lib, "steamapps", "compatdata", strconv.Itoa(appID), "pfx")
}

// lastUsed is when the game last wrote to its user data folder (logs,
// saves, settings). The folders Any Install itself writes to don't count.
func lastUsed(dir string) time.Time {
	var t time.Time
	seen := 0
	base := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil
		}
		depth := strings.Count(p, string(filepath.Separator)) - base
		if depth == 1 && d.IsDir() {
			switch strings.ToLower(d.Name()) {
			case "mods", "mods" + disabledSuffix, "anyinstall-backup":
				return filepath.SkipDir
			}
		}
		if seen++; seen > 20000 {
			return filepath.SkipAll
		}
		if info, err := d.Info(); err == nil && info.ModTime().After(t) {
			t = info.ModTime()
		}
		// Saves/<world>/<save>/<file> is deep enough.
		if d.IsDir() && depth >= 4 {
			return filepath.SkipDir
		}
		return nil
	})
	return t
}

func (g *SevenDays) Locate(env *Env) Location {
	dir, via := steamLocate(env, g.ID(), g.AppID())
	if dir == "" {
		return Location{}
	}
	if via == "custom" && !exists(filepath.Join(dir, "7DaysToDie.exe")) && !exists(filepath.Join(dir, "7DaysToDie.x86_64")) && !isDir(filepath.Join(dir, "7DaysToDie_Data")) {
		return Location{} // not the 7 Days to Die folder
	}
	ud, proton, notes := sevenDaysUserData(env, dir)
	var prefix string
	if proton {
		prefix = protonPrefix(env, dir, g.AppID())
	}
	return Location{
		Found:   true,
		Root:    ud,
		ModsDir: filepath.Join(ud, "Mods"),
		Via:     via,
		Proton:  proton,
		Prefix:  prefix,
		Notes:   notes,
		GameDir: dir,
		Backup:  filepath.Join(ud, "AnyInstall-Backup"),
		Allowed: []string{ud, dir},
	}
}

// ModInfo is the content of a ModInfo.xml that matters to us.
type ModInfo struct {
	Name, DisplayName, Version string
}

// ParseModInfo reads both the old (<ModInfo><ModInfo>...) and the new
// (<xml>...) ModInfo.xml layout.
func ParseModInfo(b []byte) ModInfo {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	d := xml.NewDecoder(bytes.NewReader(b))
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	var mi ModInfo
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var val string
		for _, a := range se.Attr {
			if strings.EqualFold(a.Name.Local, "value") {
				val = strings.TrimSpace(a.Value)
			}
		}
		if val == "" {
			continue
		}
		switch strings.ToLower(se.Name.Local) {
		case "name":
			if mi.Name == "" {
				mi.Name = val
			}
		case "displayname":
			if mi.DisplayName == "" {
				mi.DisplayName = val
			}
		case "version":
			if mi.Version == "" {
				mi.Version = val
			}
		}
	}
	return mi
}

// modRoots returns the folders that contain a marker file, skipping roots
// nested inside another root.
func modRoots(markers []string) []string {
	roots := make([]string, 0, len(markers))
	for _, m := range markers {
		roots = append(roots, dirOf(m))
	}
	sort.Slice(roots, func(i, j int) bool {
		di, dj := strings.Count(roots[i], "/"), strings.Count(roots[j], "/")
		if roots[i] == "" {
			di = -1
		}
		if roots[j] == "" {
			dj = -1
		}
		if di != dj {
			return di < dj
		}
		return roots[i] < roots[j]
	})
	var out []string
	for _, r := range roots {
		nested := false
		for _, o := range out {
			if under(r, o) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, r)
		}
	}
	return out
}

func (g *SevenDays) Analyze(src archive.Source, entries []archive.Entry) []Found {
	bases := byBase(entries)
	markers := bases["modinfo.xml"]
	if len(markers) == 0 {
		return nil
	}
	roots := modRoots(markers)
	data, _ := src.ReadFiles(markers, 1<<20)
	// Packs made for a mod launcher bring a whole game copy, including the
	// game's own mods (0_TFP_Harmony). Their mods are made for the game
	// version in the pack, which may not be the one Steam has installed.
	gameCopy := false
	for _, b := range []string{"7daystodie.exe", "7daystodie.x86_64", "assembly-csharp.dll"} {
		gameCopy = gameCopy || len(bases[b]) > 0
	}
	var out []Found
	for _, root := range roots {
		// The game already has its own mods; a second copy would be
		// loaded twice.
		if strings.HasPrefix(strings.ToLower(path.Base(root)), "0_tfp_") {
			gameCopy = true
			continue
		}
		var mi ModInfo
		for _, m := range markers {
			if dirOf(m) == root {
				mi = ParseModInfo(data[m])
				break
			}
		}
		folder := path.Base(root)
		if root == "" {
			folder = mi.Name
			if folder == "" {
				folder = fileBase(src.Path())
			}
		}
		folder = SafeFolderName(folder)
		name := mi.DisplayName
		if name == "" {
			name = mi.Name
		}
		if name == "" {
			name = folder
		}
		modID := mi.Name
		if modID == "" {
			modID = folder
		}
		f := Found{Key: root, Kind: "mod", Name: name, ModID: modID, Version: mi.Version, Folder: folder}
		for _, e := range entries {
			if !e.Dir && under(e.Name, root) && strings.EqualFold(path.Ext(e.Name), ".dll") {
				f.HasCode = true
				break
			}
		}
		out = append(out, f)
	}
	if gameCopy {
		for i := range out {
			out[i].Notes = []Note{note("warn", "game_copy")}
		}
	}
	return out
}

// modFolders lists the folders where mods may be, with their ID prefix.
func (g *SevenDays) modFolders(loc Location) []struct {
	tag, dir string
	enabled  bool
} {
	type f = struct {
		tag, dir string
		enabled  bool
	}
	out := []f{
		{"u", loc.ModsDir, true},
		{"u", loc.ModsDir + disabledSuffix, false},
	}
	if loc.GameDir != "" {
		gm := filepath.Join(loc.GameDir, "Mods")
		out = append(out, f{"g", gm, true}, f{"g", gm + disabledSuffix, false})
	}
	return out
}

func findModInfo(dir string) string {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(e.Name(), "ModInfo.xml") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// nestedModInfo looks a few levels deeper for a ModInfo.xml, which is the
// typical "unpacked one folder too deep" mistake.
func nestedModInfo(dir string) bool {
	found := false
	base := strings.Count(dir, string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || found {
			return filepath.SkipDir
		}
		if d.IsDir() && strings.Count(p, string(filepath.Separator))-base > 3 {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "ModInfo.xml") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (g *SevenDays) Installed(loc Location, _ *install.Manager) []Installed {
	var out []Installed
	for _, mf := range g.modFolders(loc) {
		ents, err := os.ReadDir(mf.dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(mf.dir, e.Name())
			it := Installed{
				ID:      mf.tag + ":" + e.Name(),
				Kind:    "mod",
				Enabled: mf.enabled,
				Paths:   []string{dir},
				Name:    e.Name(),
				ModID:   e.Name(),
			}
			if mi := findModInfo(dir); mi != "" {
				b, _ := os.ReadFile(mi)
				info := ParseModInfo(b)
				if info.Name != "" {
					it.ModID = info.Name
				}
				if info.DisplayName != "" {
					it.Name = info.DisplayName
				} else if info.Name != "" {
					it.Name = info.Name
				}
				it.Version = info.Version
				it.CanToggle = true
				if hasDLL(dir) {
					it.Notes = append(it.Notes, note("warn", "needs_eac_off"))
				}
			} else if nestedModInfo(dir) {
				it.Notes = append(it.Notes, note("error", "nested_wrong"))
				it.Kind = "broken"
			} else {
				it.Notes = append(it.Notes, note("warn", "no_modinfo"))
				it.Kind = "broken"
			}
			if strings.HasPrefix(strings.ToLower(e.Name()), "0_tfp_") {
				it.Builtin = true
				it.CanToggle = false
				it.Notes = nil
			}
			out = append(out, it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func hasDLL(dir string) bool {
	found := false
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".dll") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (g *SevenDays) match(inst []Installed, f Found) *Installed {
	for i := range inst {
		if inst[i].Kind == "mod" && !inst[i].Builtin && strings.EqualFold(inst[i].ModID, f.ModID) {
			return &inst[i]
		}
	}
	return nil
}

func (g *SevenDays) Status(loc Location, inst []Installed, f Found, _ *install.Manager) (string, string, []Note) {
	var notes []Note
	if f.HasCode {
		notes = append(notes, note("warn", "needs_eac_off"))
	}
	if it := g.match(inst, f); it != nil {
		if !it.Enabled {
			notes = append(notes, note("info", "installed_disabled"))
		}
		return versionStatus(it.Version, f.Version), it.Version, notes
	}
	return StatusNew, "", notes
}

func (g *SevenDays) Plan(loc Location, inst []Installed, f Found) (install.Plan, error) {
	if err := Ready(loc); err != nil {
		return install.Plan{}, err
	}
	dst := filepath.Join(loc.ModsDir, f.Folder)
	if it := g.match(inst, f); it != nil {
		dst = it.Paths[0]
	}
	return install.Plan{Ops: []install.Op{{Kind: install.ReplaceDir, Src: f.Key, Dst: dst}}}, nil
}

// RepairPlan fixes a mod that was unpacked one folder too deep: the inner
// mod folders are moved up into the Mods folder and the wrapper is moved
// to the backup.
func (g *SevenDays) RepairPlan(loc Location, it Installed) (archive.Source, []Found, install.Plan, error) {
	src, err := archive.Open(it.Paths[0])
	if err != nil {
		return nil, nil, install.Plan{}, err
	}
	entries, err := src.Entries()
	if err != nil {
		src.Close()
		return nil, nil, install.Plan{}, err
	}
	found := g.Analyze(src, entries)
	parent := filepath.Dir(it.Paths[0])
	// Everything is read from the wrapper folder, so the step that replaces
	// the wrapper itself (Mods/X/X -> Mods/X) has to come last.
	var plan install.Plan
	var last *install.Op
	for _, f := range found {
		op := install.Op{Kind: install.ReplaceDir, Src: f.Key, Dst: filepath.Join(parent, f.Folder)}
		if op.Dst == it.Paths[0] {
			last = &op
			continue
		}
		plan.Ops = append(plan.Ops, op)
	}
	if last != nil {
		plan.Ops = append(plan.Ops, *last)
	} else {
		plan.Ops = append(plan.Ops, install.Op{Kind: install.Remove, Dst: it.Paths[0]})
	}
	return src, found, plan, nil
}

func (g *SevenDays) SetEnabled(loc Location, it Installed, on bool) error {
	cur := it.Paths[0]
	parent := filepath.Dir(cur)
	base := filepath.Base(cur)
	var target string
	if on {
		target = filepath.Join(strings.TrimSuffix(parent, disabledSuffix), base)
	} else {
		target = filepath.Join(parent+disabledSuffix, base)
	}
	if target == cur {
		return nil
	}
	return install.Move(cur, target)
}

func (g *SevenDays) CanLaunch(Location) bool { return true }

func (g *SevenDays) Launch(Location) error { return sys.Open("steam://rungameid/251570") }

func (g *SevenDays) Tips(loc Location, installed []Found) []Note {
	var tips []Note
	code := false
	for _, f := range installed {
		if f.HasCode {
			code = true
			tips = append(tips, note("warn", "tip_7dtd_eac", "mod", f.Name))
		}
	}
	// Mods with code are made and tested on the Windows version of the
	// game. Big ones often crash the native Linux version.
	if code && runtime.GOOS == "linux" && !loc.Proton {
		tips = append(tips, note("warn", "tip_7dtd_linux_proton"))
	}
	return append(tips, note("info", "tip_7dtd_coop"))
}
