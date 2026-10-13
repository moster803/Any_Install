package games

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

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

func sevenDaysUserData() string {
	if d := os.Getenv("ANYINSTALL_7DTD_USERDATA"); d != "" {
		return d
	}
	if runtime.GOOS == "windows" {
		if app := os.Getenv("APPDATA"); app != "" {
			return filepath.Join(app, "7DaysToDie")
		}
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "7DaysToDie")
}

func (g *SevenDays) Locate(env *Env) Location {
	dir, via := steamLocate(env, g.ID(), g.AppID())
	if dir == "" {
		return Location{}
	}
	if via == "custom" && !exists(filepath.Join(dir, "7DaysToDie.exe")) && !exists(filepath.Join(dir, "7DaysToDie.x86_64")) && !isDir(filepath.Join(dir, "7DaysToDie_Data")) {
		return Location{} // not the 7 Days to Die folder
	}
	ud := sevenDaysUserData()
	return Location{
		Found:   true,
		Root:    ud,
		ModsDir: filepath.Join(ud, "Mods"),
		Via:     via,
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
	markers := byBase(entries)["modinfo.xml"]
	if len(markers) == 0 {
		return nil
	}
	roots := modRoots(markers)
	data, _ := src.ReadFiles(markers, 1<<20)
	var out []Found
	for _, root := range roots {
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
	for _, f := range installed {
		if f.HasCode {
			tips = append(tips, note("warn", "tip_7dtd_eac", "mod", f.Name))
		}
	}
	return append(tips, note("info", "tip_7dtd_coop"))
}
