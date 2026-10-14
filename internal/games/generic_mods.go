package games

import (
	"bytes"
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
)

// What a download for a generic game is. The type is Found.Parts[0].
const (
	typeLoader  = "loader"       // BepInEx or MelonLoader itself; Parts[1] = system
	typeThunder = "thunderstore" // Thunderstore package; Parts[1] = system, then its top items
	typeTree    = "tree"         // mirrors the game folder; Parts[1] = system, then its top folders
	typeDLL     = "dll"          // plugin DLLs; Parts[1] = loader the DLLs reference, then the DLLs
	typePak     = "pak"          // Unreal Engine paks; then the files
	typeFolder  = "folder"       // a mod folder for a game with a Mods folder
)

// StatusHidden marks a download that does not fit the game at all. It is
// listed with the other files that are not mods.
const StatusHidden = "hidden"

// Files that are never part of a mod a generic game can load, so a
// download made only of them is not offered.
var mediaExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true, ".webp": true,
	".pdf": true, ".doc": true, ".docx": true, ".odt": true, ".xls": true, ".xlsx": true,
	".ppt": true, ".pptx": true, ".mp3": true, ".mp4": true, ".mkv": true, ".avi": true,
	".mov": true, ".wav": true, ".flac": true, ".txt": true, ".md": true, ".rtf": true,
	".html": true, ".htm": true, ".url": true, ".iso": true, ".apk": true, ".deb": true,
	".rpm": true, ".dmg": true, ".pkg": true, ".appimage": true, ".torrent": true,
}

// windowsPrograms are files Windows runs when they are opened. Downloads
// with them are installers or tools, not mods.
var windowsPrograms = map[string]bool{
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".ps1": true, ".vbs": true, ".scr": true, ".com": true,
}

// nexusFile matches the names Nexus Mods gives its downloads:
// "<name>-<mod id>-<version>-<upload time>", e.g. "SkipIntro-123-1-2-1700000000".
var nexusFile = regexp.MustCompile(`^(.+?)-\d+-(\d+(?:-\d+)*[a-z]?)-\d{9,11}$`)

// downloadName returns a readable name and the version of a download.
// A version at the end of the name is shown separately, so it is cut off.
func downloadName(p string) (name, version string) {
	base := fileBase(p)
	if m := nexusFile.FindStringSubmatch(base); m != nil {
		return strings.TrimSpace(m[1]), strings.ReplaceAll(m[2], "-", ".")
	}
	version = VersionFromName(base)
	if version != "" && strings.HasSuffix(base, version) {
		name = strings.TrimRight(strings.TrimSuffix(base, version), " _-.")
		name = strings.TrimRight(strings.TrimSuffix(strings.TrimSuffix(name, "v"), "V"), " _-.")
	}
	if name == "" {
		name = base
	}
	return name, version
}

// Folders that stand for a place in the game folder. They are never taken
// for a wrapper around a mod.
var anchors = map[string]bool{
	"bepinex": true, "melonloader": true, "mods": true, "plugins": true, "userlibs": true,
	"userdata": true, "patchers": true, "config": true, "content": true, "paks": true,
	"~mods": true, "logicmods": true, "binaries": true,
}

func (g *Generic) Analyze(src archive.Source, entries []archive.Entry) []Found {
	if g.prof != nil && g.prof.analyze != nil {
		return g.prof.analyze(src, entries)
	}
	return analyzeGeneric(src, entries, g.appID)
}

// analyzeGeneric describes a download without knowing the game.
func analyzeGeneric(src archive.Source, entries []archive.Entry, self int) []Found {
	var files []string
	for _, e := range entries {
		if !e.Dir && !archive.Ignored(e.Name) {
			files = append(files, e.Name)
		}
	}
	if len(files) == 0 || claimed(src, entries, self) {
		return nil
	}
	modish := false
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f))
		if windowsPrograms[ext] {
			// Programs and installers are never copied into a game.
			return nil
		}
		modish = modish || !mediaExts[ext]
	}
	if !modish {
		return nil
	}
	base, version := downloadName(src.Path())
	unsure := func(f Found) Found {
		f.Unsure = true
		if f.ModID == "" {
			f.ModID = strings.ToLower(f.Folder)
		}
		return f
	}

	// Mod loaders.
	for _, f := range files {
		l := strings.ToLower(f)
		for _, marker := range []string{"bepinex/core/bepinex.preloader.dll", "bepinex/core/bepinex.dll", "bepinex/core/bepinex.core.dll"} {
			if l == marker || strings.HasSuffix(l, "/"+marker) {
				root := strings.TrimSuffix(f[:len(f)-len(marker)], "/")
				return []Found{unsure(Found{Key: root, Kind: "loader", Name: "BepInEx", ModID: "bepinex", Version: version,
					Folder: "BepInEx", Parts: loaderParts(SysBepInEx, files, root)})}
			}
		}
		for _, marker := range []string{"melonloader/melonloader.dll", "melonloader/net6/melonloader.dll", "melonloader/net35/melonloader.dll"} {
			if l == marker || strings.HasSuffix(l, "/"+marker) {
				root := strings.TrimSuffix(f[:strings.LastIndex(l, "melonloader/")], "/")
				return []Found{unsure(Found{Key: root, Kind: "loader", Name: "MelonLoader", ModID: "melonloader", Version: version,
					Folder: "MelonLoader", Parts: loaderParts(SysMelon, files, root)})}
			}
		}
		if path.Base(l) == "ue4ss.dll" {
			return []Found{unsure(Found{Kind: "loader", Name: "UE4SS", Folder: "UE4SS", Unsupported: "needs_manual"})}
		}
	}

	// Thunderstore packages describe themselves in manifest.json.
	if mf := shallowest(files, "manifest.json", 1); mf != "" {
		data, _ := src.ReadFiles([]string{mf}, 1<<20)
		var man struct {
			Name    string   `json:"name"`
			Version string   `json:"version_number"`
			Deps    []string `json:"dependencies"`
		}
		if json.Unmarshal(bytes.TrimPrefix(data[mf], []byte("\xef\xbb\xbf")), &man) == nil && man.Name != "" && man.Version != "" {
			root := dirOf(mf)
			system := SysBepInEx
			for _, d := range man.Deps {
				if strings.Contains(strings.ToLower(d), "melonloader") {
					system = SysMelon
				}
			}
			parts := []string{typeThunder, system}
			for name, isDir := range children(files, root) {
				parts = append(parts, kidRef(name, isDir))
			}
			return []Found{unsure(Found{Key: root, Kind: "mod", Name: strings.ReplaceAll(man.Name, "_", " "),
				Version: man.Version, Folder: SafeFolderName(man.Name), Parts: parts})}
		}
	}

	root, kids := contentRoot(files)
	folder := base
	if root != "" {
		folder = path.Base(root)
	}
	folder = SafeFolderName(folder)

	// Archives that mirror the game folder ("BepInEx/plugins/...").
	if kids["bepinex"].dir {
		return []Found{unsure(Found{Key: root, Kind: "mod", Name: folder, Version: version, Folder: folder,
			Parts: []string{typeTree, SysBepInEx, kids["bepinex"].name}})}
	}
	melon := []string{typeTree, SysMelon}
	for _, a := range []string{"mods", "plugins", "userlibs", "userdata"} {
		if k := kids[a]; k.dir && (a == "userdata" || hasDLLIn(files, join(root, k.name))) {
			melon = append(melon, k.name)
		}
	}
	if len(melon) > 2 && (len(melon) > 3 || !strings.EqualFold(melon[2], "userdata")) {
		return []Found{unsure(Found{Key: root, Kind: "mod", Name: folder, Version: version, Folder: folder, Parts: melon})}
	}

	// Unreal Engine paks, grouped by folder. Several groups are usually
	// alternatives to choose from.
	groups := map[string][]string{}
	var order []string
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f))
		if ext == ".pak" || ext == ".utoc" || ext == ".ucas" || ext == ".sig" {
			d := dirOf(f)
			if groups[d] == nil {
				order = append(order, d)
			}
			groups[d] = append(groups[d], f)
		}
	}
	if len(groups) > 0 {
		var out []Found
		for _, d := range order {
			if !hasPakFile(groups[d]) {
				continue
			}
			f := Found{Key: d, Kind: "mod", Name: folder, Version: version, Folder: folder, Parts: append([]string{typePak}, groups[d]...)}
			if len(order) > 1 {
				f.Name = path.Base(d)
				if d == "" {
					f.Name = folder
				}
				f.Folder = SafeFolderName(f.Name)
			}
			if strings.Contains(strings.ToLower(d), "logicmods") {
				f.Unsupported = "needs_manual"
			}
			out = append(out, unsure(f))
		}
		return out
	}

	// A Mods folder inside the download holds the mods themselves.
	if k := kids["mods"]; k.dir {
		modsRoot := join(root, k.name)
		var out []Found
		for name, isDir := range children(files, modsRoot) {
			key := join(modsRoot, name)
			if isDir {
				out = append(out, unsure(Found{Key: key, Kind: "mod", Name: name, Version: version, Folder: SafeFolderName(name), Parts: []string{typeFolder}}))
			} else if strings.EqualFold(path.Ext(name), ".dll") {
				stem := strings.TrimSuffix(name, path.Ext(name))
				out = append(out, unsure(Found{Key: key, Kind: "mod", Name: stem, Version: version, Folder: SafeFolderName(stem),
					Parts: []string{typeDLL, dllFlavor(src, []string{key}), key}}))
			}
		}
		if len(out) > 0 {
			return out
		}
	}

	// Plugin DLLs.
	var dlls []string
	for _, f := range files {
		if strings.EqualFold(path.Ext(f), ".dll") {
			dlls = append(dlls, f)
		}
	}
	if len(dlls) > 0 {
		f := Found{Key: root, Kind: "mod", Name: folder, Version: version, Folder: folder, HasCode: true,
			Parts: append([]string{typeDLL, dllFlavor(src, dlls)}, dlls...)}
		if len(dlls) == 1 && len(files) == 1 {
			stem := strings.TrimSuffix(path.Base(dlls[0]), path.Ext(dlls[0]))
			f.Name, f.Folder = stem, SafeFolderName(stem)
		}
		return []Found{unsure(f)}
	}

	// Anything else is a mod folder.
	return []Found{unsure(Found{Key: root, Kind: "mod", Name: folder, Version: version, Folder: folder, Parts: []string{typeFolder}})}
}

// loaderParts lists what a loader download puts into the game folder.
// Thunderstore's own files stay out.
func loaderParts(system string, files []string, root string) []string {
	parts := []string{typeLoader, system}
	for name, isDir := range children(files, root) {
		switch strings.ToLower(name) {
		case "manifest.json", "icon.png", "readme.md", "changelog.md":
			continue
		}
		parts = append(parts, kidRef(name, isDir))
	}
	return parts
}

// claimed reports whether a download clearly belongs to one of the games
// with their own handler, so it is not offered for other games.
func claimed(src archive.Source, entries []archive.Entry, self int) bool {
	if k := src.Kind(); k == "jar" || k == "mrpack" {
		return true
	}
	bb := byBase(entries)
	for _, m := range []string{"modinfo.xml", "fabric.mod.json", "quilt.mod.json", "pack.mcmeta"} {
		if len(bb[m]) > 0 {
			return true
		}
	}
	if len((&EldenRing{}).Analyze(src, entries)) > 0 {
		return true
	}
	return profileClaim(src, entries, self) != 0
}

type kid struct {
	name string // as in the archive
	dir  bool
}

// contentRoot skips folders that only wrap the content ("MyMod-v2/...")
// and returns the folder with the content and its children by lower-case
// name. Folders that stand for a place in the game are never skipped.
func contentRoot(files []string) (string, map[string]kid) {
	root := ""
outer:
	for {
		ks := children(files, root)
		if len(ks) == 1 {
			for name, isDir := range ks {
				if isDir && !anchors[strings.ToLower(name)] {
					root = join(root, name)
					continue outer
				}
			}
		}
		out := map[string]kid{}
		for name, isDir := range ks {
			out[strings.ToLower(name)] = kid{name: name, dir: isDir}
		}
		return root, out
	}
}

// children lists the direct children of dir among files, and whether each
// is a folder.
func children(files []string, dir string) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		rel := f
		if dir != "" {
			var ok bool
			if rel, ok = strings.CutPrefix(f, dir+"/"); !ok {
				continue
			}
		}
		if i := strings.Index(rel, "/"); i >= 0 {
			out[rel[:i]] = true
		} else if _, seen := out[rel]; !seen {
			out[rel] = false
		}
	}
	return out
}

// kidRef encodes a child for Found.Parts: "d:" for folders, "f:" for files.
func kidRef(name string, dir bool) string {
	if dir {
		return "d:" + name
	}
	return "f:" + name
}

func join(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// shallowest returns the least nested file with the given base name, at
// most maxDepth folders deep.
func shallowest(files []string, base string, maxDepth int) string {
	best, depth := "", maxDepth+1
	for _, f := range files {
		if strings.EqualFold(path.Base(f), base) {
			if d := strings.Count(f, "/"); d < depth {
				best, depth = f, d
			}
		}
	}
	return best
}

func hasDLLIn(files []string, dir string) bool {
	for _, f := range files {
		if dirOf(f) == dir && strings.EqualFold(path.Ext(f), ".dll") {
			return true
		}
	}
	return false
}

func hasPakFile(files []string) bool {
	for _, f := range files {
		if strings.EqualFold(path.Ext(f), ".pak") {
			return true
		}
	}
	return false
}

// dllFlavor tells which mod loader the DLLs are made for: the name of the
// loader's assembly is stored in every DLL that uses it.
func dllFlavor(src archive.Source, dlls []string) string {
	if len(dlls) > 4 {
		dlls = dlls[:4]
	}
	data, _ := src.ReadFiles(dlls, 16<<20)
	for _, b := range data {
		switch {
		case bytes.Contains(b, []byte("BepInEx")):
			return SysBepInEx
		case bytes.Contains(b, []byte("MelonLoader")):
			return SysMelon
		}
	}
	return ""
}

// ---------- status and plan ----------

// fits reports whether a download is the kind of mod the game loads.
func fits(system string, f Found) bool {
	if len(f.Parts) == 0 {
		return system == SysUnreal && f.Unsupported != "" // UE4SS
	}
	switch f.Parts[0] {
	case typeTree:
		// A Mods folder with DLLs also fits games with their own Mods folder.
		return f.Parts[1] == system || (system == SysModsDir && len(f.Parts) == 3 && strings.EqualFold(f.Parts[2], "mods"))
	case typeLoader, typeThunder:
		return len(f.Parts) > 1 && f.Parts[1] == system
	case typeDLL:
		flavor := f.Parts[1]
		switch system {
		case SysBepInEx, SysMelon:
			return flavor == "" || flavor == system
		case SysModsDir:
			return true
		}
	case typePak:
		return system == SysUnreal
	case typeFolder:
		return system == SysModsDir
	}
	return false
}

func (g *Generic) Status(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
	if g.prof != nil && g.prof.status != nil {
		return g.prof.status(loc, inst, f, m)
	}
	return genericStatus(loc, inst, f, m)
}

func genericStatus(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
	if !fits(loc.System, f) {
		return StatusHidden, "", nil
	}
	if f.Unsupported != "" {
		return StatusUnsupported, "", []Note{note("warn", f.Unsupported)}
	}
	if f.Kind == "loader" {
		if loaderInstalled(loc) {
			have := m.Version(f.ModID)
			if have == "" {
				return StatusInstalled, "", nil
			}
			return versionStatus(have, f.Version), have, nil
		}
		return StatusNew, "", nil
	}
	var notes []Note
	if (loc.System == SysBepInEx || loc.System == SysMelon) && !loaderInstalled(loc) {
		notes = append(notes, note("error", "needs_loader", "loader", loaderTitle(loc.System)))
	}
	for i := range inst {
		if strings.EqualFold(inst[i].ModID, f.ModID) || strings.EqualFold(inst[i].Name, f.Folder) {
			if !inst[i].Enabled {
				notes = append(notes, note("info", "installed_disabled"))
			}
			return versionStatus(inst[i].Version, f.Version), inst[i].Version, notes
		}
	}
	return StatusNew, "", notes
}

func loaderTitle(system string) string {
	if system == SysMelon {
		return "MelonLoader"
	}
	return "BepInEx"
}

func loaderInstalled(loc Location) bool {
	switch loc.System {
	case SysBepInEx:
		return isDir(filepath.Join(loc.GameDir, "BepInEx", "core"))
	case SysMelon:
		return isDir(filepath.Join(loc.GameDir, "MelonLoader"))
	}
	return true
}

func (g *Generic) Plan(loc Location, inst []Installed, f Found) (install.Plan, error) {
	if g.prof != nil && g.prof.plan != nil {
		return g.prof.plan(loc, inst, f)
	}
	return genericPlan(loc, inst, f)
}

func genericPlan(loc Location, inst []Installed, f Found) (install.Plan, error) {
	if !fits(loc.System, f) || f.Unsupported != "" || len(f.Parts) == 0 {
		return install.Plan{}, errUnsupported
	}
	game, mods := loc.GameDir, loc.ModsDir
	// An installed mod is updated where it is, also when switched off.
	dst := filepath.Join(mods, f.Folder)
	for _, it := range inst {
		if (strings.EqualFold(it.ModID, f.ModID) || strings.EqualFold(it.Name, f.Folder)) && len(it.Paths) == 1 && isDir(it.Paths[0]) {
			dst = it.Paths[0]
		}
	}
	var ops []install.Op
	switch f.Parts[0] {
	case typeLoader:
		for _, ref := range f.Parts[2:] {
			name := ref[2:]
			if ref[0] == 'd' {
				ops = append(ops, install.Op{Kind: install.MergeDir, Src: join(f.Key, name), Dst: filepath.Join(game, existingCase(game, name))})
			} else {
				ops = append(ops, install.Op{Kind: install.PutFile, Src: join(f.Key, name), Dst: filepath.Join(game, name)})
			}
		}
	case typeTree:
		for _, top := range f.Parts[2:] {
			to := filepath.Join(game, existingCase(game, top))
			if strings.EqualFold(top, "mods") && loc.System != SysBepInEx {
				to = mods
			}
			ops = append(ops, install.Op{Kind: install.MergeDir, Src: join(f.Key, top), Dst: to})
		}
	case typeThunder:
		special := map[string]string{"plugins": "plugins", "patchers": "patchers", "monomod": "monomod"}
		var rest []string
		for _, ref := range f.Parts[2:] {
			name := ref[2:]
			lower := strings.ToLower(name)
			switch {
			case ref[0] == 'd' && lower == "bepinex":
				ops = append(ops, install.Op{Kind: install.MergeDir, Src: join(f.Key, name), Dst: filepath.Join(game, "BepInEx")})
			case ref[0] == 'd' && lower == "config":
				ops = append(ops, install.Op{Kind: install.MergeDir, Src: join(f.Key, name), Dst: filepath.Join(game, "BepInEx", "config")})
			case ref[0] == 'd' && special[lower] != "":
				ops = append(ops, install.Op{Kind: install.ReplaceDir, Src: join(f.Key, name), Dst: filepath.Join(game, "BepInEx", special[lower], f.Folder)})
			case ref[0] == 'd' && lower == "core":
				// The loader's own files come from the loader download.
			default:
				rest = append(rest, ref)
			}
		}
		if len(ops) == 0 {
			// A plain package: everything goes into its plugin folder.
			ops = append(ops, install.Op{Kind: install.ReplaceDir, Src: f.Key, Dst: dst})
			break
		}
		for _, ref := range rest {
			kind := install.PutFile
			if ref[0] == 'd' {
				kind = install.ReplaceDir
			}
			ops = append(ops, install.Op{Kind: kind, Src: join(f.Key, ref[2:]), Dst: filepath.Join(dst, ref[2:])})
		}
	case typeDLL:
		dlls := f.Parts[2:]
		single := len(dlls) == 1 && dlls[0] == f.Key
		switch {
		case loc.System == SysMelon || (loc.System == SysModsDir && (f.Key == "" || single)):
			// MelonLoader wants its DLLs right in the Mods folder.
			for _, d := range dlls {
				ops = append(ops, install.Op{Kind: install.PutFile, Src: d, Dst: filepath.Join(mods, path.Base(d))})
			}
		case single:
			ops = append(ops, install.Op{Kind: install.PutFile, Src: dlls[0], Dst: filepath.Join(dst, path.Base(dlls[0]))})
		default:
			ops = append(ops, install.Op{Kind: install.ReplaceDir, Src: f.Key, Dst: dst})
		}
	case typePak:
		// Every pak mod gets its own folder in ~mods, so its files stay
		// together and it can be switched off as one.
		for _, p := range f.Parts[1:] {
			ops = append(ops, install.Op{Kind: install.PutFile, Src: p, Dst: filepath.Join(dst, path.Base(p))})
		}
	case typeFolder:
		ops = append(ops, install.Op{Kind: install.ReplaceDir, Src: f.Key, Dst: dst})
	default:
		return install.Plan{}, errUnsupported
	}
	return install.Plan{Ops: ops}, nil
}

// existingCase returns name as it is spelled in dir, so "bepinex/" from an
// archive does not create a second folder next to "BepInEx" on Linux. A
// folder that does not exist yet is spelled the way the mod loaders do.
func existingCase(dir, name string) string {
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if e.IsDir() && strings.EqualFold(e.Name(), name) {
				return e.Name()
			}
		}
	}
	for _, c := range []string{"BepInEx", "MelonLoader", "Mods", "Plugins", "UserLibs", "UserData"} {
		if strings.EqualFold(name, c) {
			return c
		}
	}
	return name
}
