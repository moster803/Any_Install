package games

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/sys"
)

// Minecraft is Minecraft: Java Edition with the official launcher. It is
// not a Steam game; its files live in .minecraft. Mods are jar files in
// .minecraft/mods and need a mod loader (Fabric, Quilt, Forge or
// NeoForge) that matches the Minecraft version.
type Minecraft struct{}

func (*Minecraft) ID() string          { return "minecraft" }
func (*Minecraft) Name() string        { return "Minecraft Java" }
func (*Minecraft) AppID() int          { return 0 }
func (*Minecraft) Processes() []string { return nil }

// Profile is an installed Minecraft version in .minecraft/versions.
type Profile struct {
	ID     string `json:"id"`
	Loader string `json:"loader"` // "" for plain Minecraft
	MC     string `json:"mc"`
}

func minecraftDefault() []string {
	if d := os.Getenv("ANYINSTALL_MINECRAFT"); d != "" {
		return []string{d}
	}
	home, _ := os.UserHomeDir()
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(os.Getenv("APPDATA"), ".minecraft")}
	}
	return []string{
		filepath.Join(home, ".minecraft"),
		filepath.Join(home, ".var", "app", "com.mojang.Minecraft", ".minecraft"),
	}
}

func (g *Minecraft) Locate(env *Env) Location {
	dir, via := "", ""
	if p := env.Custom[g.ID()]; p != "" && isDir(p) {
		if filepath.Base(p) == "mods" && isDir(filepath.Join(filepath.Dir(p), "versions")) {
			p = filepath.Dir(p) // the user picked .minecraft/mods
		}
		dir, via = p, "custom"
	} else {
		for _, d := range minecraftDefault() {
			if isDir(d) {
				dir, via = d, "default"
				break
			}
		}
	}
	if dir == "" {
		return Location{}
	}
	loc := Location{
		Found:   true,
		Root:    dir,
		ModsDir: filepath.Join(dir, "mods"),
		Via:     via,
		GameDir: dir,
		Backup:  filepath.Join(dir, "AnyInstall-Backup"),
		Allowed: []string{dir},
	}
	hasLoader := false
	for _, p := range Profiles(dir) {
		if p.Loader != "" {
			hasLoader = true
			loc.Notes = append(loc.Notes, note("info", "mc_loader_found", "loader", p.Loader, "mc", p.MC, "profile", p.ID))
		}
	}
	if !hasLoader {
		loc.Notes = append(loc.Notes, note("warn", "mc_no_loader"))
	}
	return loc
}

// Profiles lists the installed Minecraft versions and mod loaders.
func Profiles(mcDir string) []Profile {
	ents, err := os.ReadDir(filepath.Join(mcDir, "versions"))
	if err != nil {
		return nil
	}
	var out []Profile
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		b, err := os.ReadFile(filepath.Join(mcDir, "versions", e.Name(), e.Name()+".json"))
		if err != nil {
			continue
		}
		var j struct {
			ID           string `json:"id"`
			InheritsFrom string `json:"inheritsFrom"`
		}
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		if j.ID == "" {
			j.ID = e.Name()
		}
		p := Profile{ID: j.ID, MC: j.InheritsFrom}
		id := strings.ToLower(j.ID)
		switch {
		case strings.Contains(id, "neoforge"):
			p.Loader = "neoforge"
		case strings.Contains(id, "forge"):
			p.Loader = "forge"
		case strings.HasPrefix(id, "fabric-loader"):
			p.Loader = "fabric"
		case strings.HasPrefix(id, "quilt-loader"):
			p.Loader = "quilt"
		}
		if p.MC == "" {
			p.MC = j.ID
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i].MC, out[j].MC) > 0 })
	return out
}

// loaderFits reports whether mods for loader `need` run on a profile.
func loaderFits(need, have string) bool {
	return need == have || (need == "fabric" && have == "quilt")
}

// compatNotes checks a mod against the installed profiles.
func compatNotes(profiles []Profile, loader, mc string) []Note {
	if loader == "" {
		return nil
	}
	var fitting []Profile
	for _, p := range profiles {
		if loaderFits(loader, p.Loader) {
			fitting = append(fitting, p)
		}
	}
	if len(fitting) == 0 {
		return []Note{note("error", "mc_needs_loader", "loader", loader, "mc", DescribeMC(mc))}
	}
	var have []string
	for _, p := range fitting {
		if ok, _ := MatchMC(mc, p.MC); ok {
			return nil
		}
		have = append(have, p.MC)
	}
	return []Note{note("error", "mc_wrong_version", "loader", loader, "need", DescribeMC(mc), "have", strings.Join(have, ", "))}
}

func (g *Minecraft) Analyze(src archive.Source, entries []archive.Entry) []Found {
	base := filepath.Base(src.Path())
	switch src.Kind() {
	case "jar":
		files, _ := src.ReadFiles(jarMetaFiles, 4<<20)
		if m, ok := ParseJarFiles(files); ok {
			return []Found{jarFound("", base, m)}
		}
		return nil
	case "mrpack":
		return []Found{{Kind: "modpack", Name: fileBase(src.Path()), ModID: strings.ToLower(base), Unsupported: "mc_modpack"}}
	}

	bb := byBase(entries)
	if len(bb["modrinth.index.json"]) > 0 {
		return []Found{{Kind: "modpack", Name: fileBase(src.Path()), ModID: strings.ToLower(base), Unsupported: "mc_modpack"}}
	}
	for _, m := range bb["manifest.json"] {
		if strings.Count(m, "/") > 1 {
			continue
		}
		files, _ := src.ReadFiles([]string{m}, 1<<20)
		if strings.Contains(string(files[m]), "minecraftModpack") {
			return []Found{{Kind: "modpack", Name: fileBase(src.Path()), ModID: strings.ToLower(base), Unsupported: "mc_modpack"}}
		}
	}

	// Jars packed inside a zip/7z/rar.
	var jars []string
	for _, e := range entries {
		if !e.Dir && strings.EqualFold(path.Ext(e.Name), ".jar") && e.Size < 64<<20 {
			jars = append(jars, e.Name)
		}
	}
	if len(jars) > 0 {
		data, _ := src.ReadFiles(jars, 64<<20)
		var out []Found
		for _, j := range jars {
			if m, ok := ParseJarBytes(data[j]); ok {
				out = append(out, jarFound(j, path.Base(j), m))
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Resource packs, data packs and shader packs.
	whole := src.Kind() == "zip"
	for _, m := range bb["pack.mcmeta"] {
		root := dirOf(m)
		if strings.Count(root, "/") > 0 {
			continue
		}
		hasAssets, hasData := false, false
		for _, e := range entries {
			if e.Dir && dirOf(e.Name) == root {
				switch strings.ToLower(path.Base(e.Name)) {
				case "assets":
					hasAssets = true
				case "data":
					hasData = true
				}
			}
		}
		if hasData && !hasAssets {
			return []Found{{Kind: "datapack", Name: fileBase(src.Path()), ModID: strings.ToLower(base), Unsupported: "mc_datapack"}}
		}
		return []Found{packFound("resourcepack", src, root, whole && root == "")}
	}
	for _, e := range entries {
		if e.Dir && strings.EqualFold(path.Base(e.Name), "shaders") && strings.Count(e.Name, "/") <= 1 {
			root := dirOf(e.Name)
			return []Found{packFound("shaderpack", src, root, whole && root == "")}
		}
	}
	return nil
}

func jarFound(key, file string, m JarMeta) Found {
	return Found{
		Key: key, Kind: "jar", Name: m.Name, ModID: m.ID, Version: m.Version,
		Loader: m.Loader, MCVersion: m.MC, Deps: m.Deps, Folder: file,
	}
}

func packFound(kind string, src archive.Source, root string, whole bool) Found {
	f := Found{Key: root, Kind: kind}
	if whole {
		f.Folder = filepath.Base(src.Path())
		f.Parts = []string{"self"}
		f.Name = fileBase(src.Path())
	} else {
		name := path.Base(root)
		if root == "" {
			name = fileBase(src.Path())
		}
		f.Folder = SafeFolderName(name)
		f.Name = name
	}
	f.ModID = strings.ToLower(f.Folder)
	return f
}

var jarCache sync.Map // path|size|mtime -> JarMeta

func readJarCached(p string) (JarMeta, bool) {
	st, err := os.Stat(p)
	if err != nil {
		return JarMeta{}, false
	}
	key := fmt.Sprintf("%s|%d|%d", p, st.Size(), st.ModTime().UnixNano())
	if v, ok := jarCache.Load(key); ok {
		m := v.(JarMeta)
		return m, m.ID != ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return JarMeta{}, false
	}
	m, _ := ParseJarBytes(b)
	jarCache.Store(key, m)
	return m, m.ID != ""
}

func (g *Minecraft) Installed(loc Location, _ *install.Manager) []Installed {
	var out []Installed
	profiles := Profiles(loc.Root)
	if ents, err := os.ReadDir(loc.ModsDir); err == nil {
		for _, e := range ents {
			name := e.Name()
			lower := strings.ToLower(name)
			enabled := strings.HasSuffix(lower, ".jar")
			if e.IsDir() || (!enabled && !strings.HasSuffix(lower, ".jar.disabled")) {
				continue
			}
			full := filepath.Join(loc.ModsDir, name)
			file := strings.TrimSuffix(name, ".disabled")
			it := Installed{
				ID: "mod:" + strings.ToLower(file), Kind: "jar", Name: strings.TrimSuffix(file, ".jar"),
				ModID: strings.ToLower(file), Enabled: enabled, CanToggle: true, Paths: []string{full},
			}
			if m, ok := readJarCached(full); ok {
				it.Name, it.ModID, it.Version, it.Loader, it.MCVersion = m.Name, m.ID, m.Version, m.Loader, m.MC
				it.MCLabel = DescribeMC(m.MC)
				if enabled {
					it.Notes = compatNotes(profiles, m.Loader, m.MC)
					// Fabric refuses to start with a mod for another version.
					if len(it.Notes) > 0 && it.Notes[0].Code == "mc_wrong_version" && (m.Loader == "fabric" || m.Loader == "quilt") {
						it.Notes = append(it.Notes, note("error", "mc_blocks_start"))
					}
				}
			}
			out = append(out, it)
		}
	}
	for _, d := range []struct{ kind, dir, prefix string }{
		{"resourcepack", "resourcepacks", "rp:"},
		{"shaderpack", "shaderpacks", "sp:"},
	} {
		ents, err := os.ReadDir(filepath.Join(loc.Root, d.dir))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() && !strings.EqualFold(filepath.Ext(e.Name()), ".zip") {
				continue
			}
			out = append(out, Installed{
				ID: d.prefix + strings.ToLower(e.Name()), ModID: strings.ToLower(e.Name()), Kind: d.kind,
				Name: strings.TrimSuffix(e.Name(), ".zip"), Enabled: true,
				Paths: []string{filepath.Join(loc.Root, d.dir, e.Name())},
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

func (g *Minecraft) Status(loc Location, inst []Installed, f Found, _ *install.Manager) (string, string, []Note) {
	switch f.Kind {
	case "modpack", "datapack":
		return StatusUnsupported, "", []Note{note("warn", f.Unsupported)}
	case "resourcepack", "shaderpack":
		for _, it := range inst {
			if it.Kind == f.Kind && strings.EqualFold(it.ModID, f.ModID) {
				return StatusInstalled, "", nil
			}
		}
		var notes []Note
		if f.Kind == "shaderpack" {
			notes = append(notes, note("info", "mc_needs_iris"))
		}
		return StatusNew, "", notes
	}
	notes := compatNotes(Profiles(loc.Root), f.Loader, f.MCVersion)
	if f.Loader == "fabric" || f.Loader == "quilt" {
		for _, d := range f.Deps {
			if d != "fabric-api" && d != "fabric" {
				continue
			}
			found := false
			for _, it := range inst {
				if it.ModID == "fabric-api" || it.ModID == "fabric" {
					found = true
				}
			}
			if !found {
				notes = append(notes, note("info", "mc_needs_fabric_api"))
			}
			break
		}
	}
	for _, it := range inst {
		if it.Kind == "jar" && strings.EqualFold(it.ModID, f.ModID) {
			return versionStatus(it.Version, f.Version), it.Version, notes
		}
	}
	return StatusNew, "", notes
}

func (g *Minecraft) Plan(loc Location, inst []Installed, f Found) (install.Plan, error) {
	var p install.Plan
	switch f.Kind {
	case "jar":
		dst := filepath.Join(loc.ModsDir, f.Folder)
		// Older versions of the same mod have to go, or Minecraft crashes
		// with "duplicate mod".
		for _, it := range inst {
			if it.Kind == "jar" && strings.EqualFold(it.ModID, f.ModID) && it.Paths[0] != dst {
				p.Ops = append(p.Ops, install.Op{Kind: install.Remove, Dst: it.Paths[0]})
			}
		}
		if f.Key == "" {
			p.Ops = append(p.Ops, install.Op{Kind: install.PutSelf, Dst: dst})
		} else {
			p.Ops = append(p.Ops, install.Op{Kind: install.PutFile, Src: f.Key, Dst: dst})
		}
	case "resourcepack", "shaderpack":
		dst := filepath.Join(loc.Root, f.Kind+"s", f.Folder)
		if len(f.Parts) > 0 && f.Parts[0] == "self" {
			p.Ops = append(p.Ops, install.Op{Kind: install.PutSelf, Dst: dst})
		} else {
			p.Ops = append(p.Ops, install.Op{Kind: install.ReplaceDir, Src: f.Key, Dst: dst})
		}
	default:
		return p, errUnsupported
	}
	return p, nil
}

func (g *Minecraft) SetEnabled(loc Location, it Installed, on bool) error {
	cur := it.Paths[0]
	var target string
	if on {
		target = strings.TrimSuffix(cur, ".disabled")
	} else if !strings.HasSuffix(cur, ".disabled") {
		target = cur + ".disabled"
	}
	if target == "" || target == cur {
		return nil
	}
	return install.Move(cur, target)
}

// launcherExe finds the official Minecraft Launcher on Windows.
func (g *Minecraft) launcherExe() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, p := range []string{
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Minecraft Launcher", "MinecraftLauncher.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Minecraft Launcher", "MinecraftLauncher.exe"),
		`C:\XboxGames\Minecraft Launcher\Content\Minecraft.exe`,
	} {
		if exists(p) {
			return p
		}
	}
	return ""
}

func (g *Minecraft) CanLaunch(Location) bool { return g.launcherExe() != "" }

func (g *Minecraft) Launch(Location) error {
	exe := g.launcherExe()
	if exe == "" {
		return os.ErrNotExist
	}
	return sys.Start(exe)
}

func (g *Minecraft) Tips(loc Location, installed []Found) []Note {
	var tips []Note
	if id := fittingProfile(Profiles(loc.Root), installed); id != "" {
		tips = append(tips, note("info", "tip_mc_profile", "profile", launcherProfileName(loc.Root, id)))
	}
	return append(tips, note("info", "tip_mc_coop"))
}

// fittingProfile returns the first installed version that runs the mods.
func fittingProfile(profiles []Profile, mods []Found) string {
	for _, f := range mods {
		if f.Kind != "jar" {
			continue
		}
		for _, p := range profiles {
			if ok, _ := MatchMC(f.MCVersion, p.MC); ok && loaderFits(f.Loader, p.Loader) {
				return p.ID
			}
		}
	}
	return ""
}

// launcherProfileName returns the name the Minecraft Launcher shows for a
// version (for example "fabric-loader-1.21.1"), or the version id.
func launcherProfileName(mcDir, versionID string) string {
	for _, f := range launcherProfileFiles {
		b, err := os.ReadFile(filepath.Join(mcDir, f))
		if err != nil {
			continue
		}
		var j struct {
			Profiles map[string]struct {
				Name          string `json:"name"`
				LastVersionID string `json:"lastVersionId"`
			} `json:"profiles"`
		}
		if json.Unmarshal(b, &j) != nil {
			continue
		}
		for _, p := range j.Profiles {
			if p.LastVersionID == versionID && p.Name != "" {
				return p.Name
			}
		}
	}
	return versionID
}
