package games

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/paths"
)

// profile holds what Any Install knows about one popular game whose mods
// do not follow one of the generic mod systems, or that needs a mod
// loader first, or whose mods it must not touch. Every hook is optional;
// without it the generic behaviour applies.
type profile struct {
	appID int
	name  string
	// family groups games that share mods (ETS2 and ATS), so they do not
	// take each other's downloads away.
	family string
	// refuse names why the game needs a real mod manager instead.
	refuse string
	tip    string // note code shown after an install
	// tracked games get mods whose files spread over the game folder.
	// Their file lists are remembered, see install.TrackedMod.
	tracked bool
	// claim reports whether a download clearly belongs to this game, so
	// other games do not offer it.
	claim   func(src archive.Source, entries []archive.Entry) bool
	locate  func(env *Env, g *Generic) Location
	analyze func(src archive.Source, entries []archive.Entry) []Found
	status  func(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note)
	plan    func(loc Location, inst []Installed, f Found) (install.Plan, error)
}

var profiles []*profile

func profileFor(appID int) *profile {
	for _, p := range profiles {
		if p.appID == appID {
			return p
		}
	}
	return nil
}

// profileClaim returns the app id of the game a download clearly belongs
// to, not counting self and its family, or 0.
func profileClaim(src archive.Source, entries []archive.Entry, self int) int {
	mine := profileFor(self)
	for _, p := range profiles {
		if p.appID == self || p.claim == nil || (mine != nil && mine.family != "" && mine.family == p.family) {
			continue
		}
		if p.claim(src, entries) {
			return p.appID
		}
	}
	return 0
}

func init() {
	// Games whose mods need load orders, merging or a launcher of their
	// own. Copying files would not work or would break things.
	for _, r := range []struct {
		id        int
		name, why string
	}{
		{489830, "The Elder Scrolls V: Skyrim Special Edition", "loadorder"},
		{72850, "The Elder Scrolls V: Skyrim", "loadorder"},
		{22330, "The Elder Scrolls IV: Oblivion", "loadorder"},
		{377160, "Fallout 4", "loadorder"},
		{22380, "Fallout: New Vegas", "loadorder"},
		{22300, "Fallout 3", "loadorder"},
		{22370, "Fallout 3: Game of the Year Edition", "loadorder"},
		{1716740, "Starfield", "loadorder"},
		{1086940, "Baldur's Gate 3", "loadorder"},
		{435150, "Divinity: Original Sin 2", "loadorder"},
		{292030, "The Witcher 3: Wild Hunt", "special"},
		{271590, "Grand Theft Auto V", "special"},
		{3240220, "Grand Theft Auto V Enhanced", "special"},
		{526870, "Satisfactory", "special"},
		{1281930, "tModLoader", "special"},
		{646570, "Slay the Spire", "special"},
		{427520, "Factorio", "ingame"},
		{949230, "Cities: Skylines II", "ingame"},
		{281990, "Stellaris", "ingame"},
		{394360, "Hearts of Iron IV", "ingame"},
		{1158310, "Crusader Kings III", "ingame"},
		{236850, "Europa Universalis IV", "ingame"},
	} {
		profiles = append(profiles, &profile{appID: r.id, name: r.name, refuse: "refused_" + r.why})
	}

	// Games made for BepInEx. They are offered even before BepInEx is
	// there, with the right BepInEx pack to download.
	for _, b := range []struct {
		id         int
		name, pack string
	}{
		{892970, "Valheim", "https://thunderstore.io/c/valheim/p/denikson/BepInExPack_Valheim/"},
		{1966720, "Lethal Company", "https://thunderstore.io/c/lethal-company/p/BepInEx/BepInExPack/"},
		{632360, "Risk of Rain 2", "https://thunderstore.io/c/riskofrain2/p/bbepis/BepInExPack/"},
	} {
		profiles = append(profiles, &profile{appID: b.id, name: b.name, locate: bepinexLocate(b.pack)})
	}

	profiles = append(profiles,
		markerProfile(413150, "Stardew Valley", "smapi", "Mods", marker{
			file: "manifest.json", parse: parseSMAPI,
			loader:     func(dir string) bool { return exists(filepath.Join(dir, "StardewModdingAPI.dll")) },
			loaderName: "SMAPI", loaderURL: "https://smapi.io/",
		}),
		markerProfile(294100, "RimWorld", "rimworld", "Mods", marker{
			file: "about.xml", about: true, parse: parseRimWorld, tip: "tip_rimworld",
		}),
		markerProfile(261550, "Mount & Blade II: Bannerlord", "bannerlord", "Modules", marker{
			file: "submodule.xml", parse: parseBannerlord, folderIsID: true, tip: "tip_bannerlord",
		}),
		scsProfile(227300, "Euro Truck Simulator 2"),
		scsProfile(270880, "American Truck Simulator"),
		simsProfile(),
		cyberpunkProfile(),
	)
}

// ---------- BepInEx games ----------

func bepinexLocate(pack string) func(env *Env, g *Generic) Location {
	return func(env *Env, g *Generic) Location {
		dir := g.app.Dir
		loc := g.baseLocation(SysBepInEx, filepath.Join(dir, "BepInEx", "plugins"))
		g.lists = []modList{{dir: loc.ModsDir, disabled: filepath.Join(dir, "BepInEx", "plugins_disabled"), files: systemFiles[SysBepInEx]}}
		loc.Notes = append(loc.Notes, note("info", "hint_pick"))
		switch {
		case !g.lay.loader:
			loc.Notes = append(loc.Notes, note("warn", "loader_missing", "loader", "BepInEx", "url", pack))
		case g.lay.proton():
			loc.Notes = append(loc.Notes, protonOverride(SysBepInEx))
		case runtime.GOOS == "linux" && exists(filepath.Join(dir, "start_game_bepinex.sh")):
			// The Linux version of Valheim starts BepInEx with a script.
			loc.Notes = append(loc.Notes, note("info", "linux_bepinex", "option", "./start_game_bepinex.sh %command%"))
		}
		return loc
	}
}

// ---------- games whose mods are folders with a marker file ----------

type marker struct {
	file  string // marker file name, lower case
	about bool   // the marker is in an About folder (RimWorld)
	parse func(b []byte) (id, name, version string, builtin bool)
	// folderIsID: the mod folder must be named like the mod's id.
	folderIsID bool
	loader     func(gameDir string) bool
	loaderName string
	loaderURL  string
	tip        string
}

// rootOf returns the mod folder of a marker file inside a download.
func (mk marker) rootOf(name string) (string, bool) {
	d := dirOf(name)
	if mk.about {
		if !strings.EqualFold(path.Base(d), "about") {
			return "", false
		}
		return dirOf(d), true
	}
	return d, true
}

func (mk marker) analyze(src archive.Source, entries []archive.Entry) []Found {
	type cand struct{ root, file string }
	var cands []cand
	for _, e := range entries {
		if !e.Dir && windowsPrograms[strings.ToLower(path.Ext(e.Name))] {
			return nil // programs are never copied into a game
		}
		if e.Dir || !strings.EqualFold(e.Base(), mk.file) {
			continue
		}
		if root, ok := mk.rootOf(e.Name); ok {
			cands = append(cands, cand{root, e.Name})
		}
	}
	if len(cands) == 0 {
		return nil
	}
	// A mod folder inside another mod folder belongs to that mod.
	sort.Slice(cands, func(i, j int) bool {
		return strings.Count(cands[i].root, "/") < strings.Count(cands[j].root, "/") || cands[i].root == ""
	})
	var keep []cand
	for _, c := range cands {
		nested := false
		for _, k := range keep {
			if k.root == "" || under(c.root, k.root) {
				nested = true
			}
		}
		if !nested {
			keep = append(keep, c)
		}
	}
	names := make([]string, len(keep))
	for i, c := range keep {
		names[i] = c.file
	}
	data, _ := src.ReadFiles(names, 1<<20)
	var out []Found
	for _, c := range keep {
		id, name, version, _ := mk.parse(data[c.file])
		if id == "" {
			continue
		}
		folder := path.Base(c.root)
		if c.root == "" {
			folder = fileBase(src.Path())
		}
		if mk.folderIsID {
			folder = id
		}
		if name == "" {
			name = folder
		}
		out = append(out, Found{Key: c.root, Kind: "mod", Name: name, ModID: strings.ToLower(id), Version: version, Folder: SafeFolderName(folder)})
	}
	return out
}

// describe reads an installed mod folder.
func (mk marker) describe(p string, isDir bool) (entryInfo, bool) {
	if !isDir {
		return entryInfo{}, false
	}
	base := filepath.Base(p)
	file := p
	if mk.about {
		file = gameDirEntry(p, "about")
	}
	if file != "" {
		file = gameDirEntry(file, mk.file)
	}
	if file == "" {
		return entryInfo{name: base}, true
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return entryInfo{name: base}, true
	}
	id, name, version, builtin := mk.parse(b)
	return entryInfo{id: id, name: name, version: version, builtin: builtin}, true
}

func markerProfile(appID int, name, system, sub string, mk marker) *profile {
	p := &profile{appID: appID, name: name, tip: mk.tip}
	p.claim = func(src archive.Source, entries []archive.Entry) bool { return len(mk.analyze(src, entries)) > 0 }
	p.analyze = mk.analyze
	p.locate = func(env *Env, g *Generic) Location {
		dir := g.app.Dir
		mods := filepath.Join(dir, existingCase(dir, sub))
		loc := g.baseLocation(system, mods)
		g.lists = []modList{{dir: mods, disabled: mods + "_disabled", describe: mk.describe}}
		if mk.loader != nil && !mk.loader(dir) {
			// SMAPI comes with an installer, which the user runs.
			loc.Notes = append(loc.Notes, note("warn", "loader_missing_installer", "loader", mk.loaderName, "url", mk.loaderURL))
		} else if system == "smapi" && runtime.GOOS == "windows" {
			// SMAPI is started instead of the game through this option.
			loc.Notes = append(loc.Notes, note("info", "smapi_launch", "option", `"`+filepath.Join(dir, "StardewModdingAPI.exe")+`" %command%`))
		}
		return loc
	}
	p.status = func(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
		var notes []Note
		if mk.loader != nil && !mk.loader(loc.GameDir) {
			notes = append(notes, note("error", "needs_loader", "loader", mk.loaderName))
		}
		for _, it := range inst {
			if it.ModID == f.ModID {
				if !it.Enabled {
					notes = append(notes, note("info", "installed_disabled"))
				}
				return versionStatus(it.Version, f.Version), it.Version, notes
			}
		}
		return StatusNew, "", notes
	}
	p.plan = func(loc Location, inst []Installed, f Found) (install.Plan, error) {
		dst := filepath.Join(loc.ModsDir, f.Folder)
		for _, it := range inst {
			if it.ModID == f.ModID && !it.Builtin && len(it.Paths) == 1 {
				dst = it.Paths[0] // updated where it is, also when switched off
			}
		}
		return install.Plan{Ops: []install.Op{{Kind: install.ReplaceDir, Src: f.Key, Dst: dst}}}, nil
	}
	return p
}

// parseSMAPI reads a Stardew Valley manifest.json. Thunderstore packages
// also have one, but without UniqueID.
func parseSMAPI(b []byte) (id, name, version string, builtin bool) {
	var m struct {
		UniqueID string          `json:"UniqueID"`
		Name     string          `json:"Name"`
		Version  json.RawMessage `json:"Version"`
	}
	if json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &m) != nil {
		return
	}
	var v string
	if json.Unmarshal(m.Version, &v) != nil {
		var old struct{ MajorVersion, MinorVersion, PatchVersion int }
		if json.Unmarshal(m.Version, &old) == nil {
			v = strings.Join([]string{strconv.Itoa(old.MajorVersion), strconv.Itoa(old.MinorVersion), strconv.Itoa(old.PatchVersion)}, ".")
		}
	}
	return strings.TrimSpace(m.UniqueID), m.Name, v, false
}

// parseRimWorld reads About/About.xml.
func parseRimWorld(b []byte) (id, name, version string, builtin bool) {
	var m struct {
		PackageID  string `xml:"packageId"`
		Name       string `xml:"name"`
		ModVersion string `xml:"modVersion"`
	}
	if decodeXML(b, &m) != nil {
		return
	}
	return strings.TrimSpace(m.PackageID), strings.TrimSpace(m.Name), strings.TrimSpace(m.ModVersion), false
}

// parseBannerlord reads SubModule.xml. Official modules say so.
func parseBannerlord(b []byte) (id, name, version string, builtin bool) {
	var m struct {
		ID       valueTag `xml:"Id"`
		Name     valueTag `xml:"Name"`
		Version  valueTag `xml:"Version"`
		Official valueTag `xml:"Official"`
	}
	if decodeXML(b, &m) != nil {
		return
	}
	return m.ID.Value, m.Name.Value, strings.TrimPrefix(strings.TrimPrefix(m.Version.Value, "v"), "e"), strings.EqualFold(m.Official.Value, "true")
}

type valueTag struct {
	Value string `xml:"value,attr"`
}

func decodeXML(b []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	return d.Decode(v)
}

// ---------- user folders outside the game ----------

// docsFolder returns the documents folder the game uses: the user's on
// Windows, the one inside the Proton prefix for the Windows version on
// Linux, or "" for a game without a Windows version installed.
func docsFolder(env *Env, g *Generic) (docs, prefix string) {
	if runtime.GOOS == "windows" || os.Getenv("ANYINSTALL_DOCUMENTS") != "" {
		return paths.DocumentsDir(), ""
	}
	if !g.lay.windows {
		return "", ""
	}
	pfx := protonPrefix(env, g.app.Dir, g.appID)
	return filepath.Join(prefixUser(pfx), "Documents"), pfx
}

// userLocation is a location whose mods live in a folder of the user, not
// of the game.
func (g *Generic) userLocation(system, root, mods, prefix string) Location {
	loc := Location{
		Found:   true,
		Root:    root,
		ModsDir: mods,
		Via:     "steam",
		Proton:  prefix != "",
		Prefix:  prefix,
		System:  system,
		GameDir: g.app.Dir,
		Backup:  filepath.Join(root, "AnyInstall-Backup"),
		Allowed: []string{root},
	}
	if Ready(loc) != nil {
		loc.Notes = append(loc.Notes, note("warn", "start_once", "game", g.Name()))
	}
	return loc
}

// ---------- Euro Truck Simulator 2 and American Truck Simulator ----------

// scsProfile: mods are .scs files (or zip files with a mod inside) in the
// game's mod folder, switched on per profile in the game.
func scsProfile(appID int, name string) *profile {
	p := &profile{appID: appID, name: name, family: "scs", tip: "tip_scs"}
	p.claim = func(src archive.Source, entries []archive.Entry) bool {
		bb := byBase(entries)
		if len(bb["manifest.sii"]) > 0 {
			return true
		}
		for _, e := range entries {
			if !e.Dir && strings.EqualFold(path.Ext(e.Name), ".scs") {
				return true
			}
		}
		return false
	}
	p.locate = func(env *Env, g *Generic) Location {
		docs, prefix := docsFolder(env, g)
		root := filepath.Join(docs, name)
		if docs == "" {
			// The Linux version keeps its files in ~/.local/share.
			home, _ := os.UserHomeDir()
			root = filepath.Join(home, ".local", "share", name)
		}
		loc := g.userLocation("scs", root, filepath.Join(root, "mod"), prefix)
		g.lists = []modList{{dir: loc.ModsDir, disabled: filepath.Join(root, "mod_disabled"), files: []string{".scs", ".zip"}}}
		loc.Notes = append(loc.Notes, note("info", "hint_pick"))
		return loc
	}
	p.analyze = func(src archive.Source, entries []archive.Entry) []Found {
		base, version := downloadName(src.Path())
		self := Found{Key: "", Kind: "mod", Name: base, ModID: strings.ToLower(base), Version: version,
			Folder: filepath.Base(src.Path()), Parts: []string{"self"}, Unsure: true}
		if src.Kind() == "file" {
			if strings.EqualFold(filepath.Ext(src.Path()), ".scs") {
				return []Found{self}
			}
			return nil
		}
		var files, scs []string
		for _, e := range entries {
			if e.Dir || archive.Ignored(e.Name) {
				continue
			}
			files = append(files, e.Name)
			ext := strings.ToLower(path.Ext(e.Name))
			if programExts[ext] {
				return nil
			}
			if ext == ".scs" {
				scs = append(scs, e.Name)
			}
		}
		if len(scs) > 0 {
			stem := strings.TrimSuffix(path.Base(scs[0]), path.Ext(scs[0]))
			return []Found{{Kind: "mod", Name: base, ModID: strings.ToLower(stem), Version: version,
				Folder: SafeFolderName(base), Parts: append([]string{"scs"}, scs...), Unsure: true}}
		}
		_, kids := contentRoot(files)
		if mf := shallowest(files, "manifest.sii", 0); mf != "" || kids["def"].dir {
			// A mod packed as a plain zip works as it is. Other archives
			// would have to be repacked.
			if src.Kind() == "zip" && (mf != "" || children(files, "")["def"]) {
				return []Found{self}
			}
			self.Unsupported = "needs_manual"
			return []Found{self}
		}
		return nil
	}
	p.status = func(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
		if f.Unsupported != "" {
			return StatusUnsupported, "", []Note{note("warn", f.Unsupported)}
		}
		for _, it := range inst {
			if it.ModID == f.ModID {
				return versionStatus(it.Version, f.Version), it.Version, nil
			}
		}
		return StatusNew, "", nil
	}
	p.plan = func(loc Location, inst []Installed, f Found) (install.Plan, error) {
		if err := Ready(loc); err != nil {
			return install.Plan{}, err
		}
		if len(f.Parts) == 0 || f.Unsupported != "" {
			return install.Plan{}, errUnsupported
		}
		if f.Parts[0] == "self" {
			return install.Plan{Ops: []install.Op{{Kind: install.PutSelf, Dst: filepath.Join(loc.ModsDir, f.Folder)}}}, nil
		}
		var ops []install.Op
		for _, s := range f.Parts[1:] {
			ops = append(ops, install.Op{Kind: install.PutFile, Src: s, Dst: filepath.Join(loc.ModsDir, path.Base(s))})
		}
		return install.Plan{Ops: ops}, nil
	}
	return p
}

// ---------- The Sims 4 ----------

var (
	simsModExts  = map[string]bool{".package": true, ".ts4script": true}
	simsTrayExts = map[string]bool{".trayitem": true, ".blueprint": true, ".bpi": true, ".hhi": true, ".sgi": true,
		".householdbinary": true, ".room": true, ".rmi": true}
)

func simsProfile() *profile {
	p := &profile{appID: 1222670, name: "The Sims 4", tip: "tip_sims"}
	p.claim = func(src archive.Source, entries []archive.Entry) bool {
		for _, e := range entries {
			if !e.Dir && simsModExts[strings.ToLower(path.Ext(e.Name))] {
				return true
			}
		}
		return false
	}
	p.locate = func(env *Env, g *Generic) Location {
		docs, prefix := docsFolder(env, g)
		if docs == "" {
			return Location{}
		}
		root := simsFolder(filepath.Join(docs, "Electronic Arts"))
		loc := g.userLocation("sims4", root, filepath.Join(root, "Mods"), prefix)
		if !isDir(root) && Ready(loc) == nil {
			// The game creates the folder in the language it runs in.
			loc.WaitStart = true
			loc.Notes = append(loc.Notes, note("warn", "start_once", "game", g.Name()))
		}
		g.lists = []modList{{dir: loc.ModsDir, disabled: filepath.Join(root, "Mods_disabled"), files: []string{".package", ".ts4script"}}}
		return loc
	}
	p.analyze = func(src archive.Source, entries []archive.Entry) []Found {
		base, version := downloadName(src.Path())
		var files []string
		for _, e := range entries {
			if e.Dir || archive.Ignored(e.Name) {
				continue
			}
			ext := strings.ToLower(path.Ext(e.Name))
			if programExts[ext] {
				return nil
			}
			if simsModExts[ext] || simsTrayExts[ext] {
				files = append(files, e.Name)
			}
		}
		if len(files) == 0 {
			return nil
		}
		f := Found{Kind: "mod", Name: base, ModID: strings.ToLower(SafeFolderName(base)), Version: version,
			Folder: SafeFolderName(base), Parts: append([]string{"sims"}, files...)}
		if src.Kind() == "file" {
			f.Folder, f.Parts = filepath.Base(src.Path()), []string{"self"}
			f.ModID = strings.ToLower(strings.TrimSuffix(f.Folder, filepath.Ext(f.Folder)))
		}
		return []Found{f}
	}
	p.status = func(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
		for _, it := range inst {
			if it.ModID == f.ModID {
				return versionStatus(it.Version, f.Version), it.Version, nil
			}
		}
		return StatusNew, "", nil
	}
	p.plan = func(loc Location, inst []Installed, f Found) (install.Plan, error) {
		if err := Ready(loc); err != nil {
			return install.Plan{}, err
		}
		if len(f.Parts) == 0 {
			return install.Plan{}, errUnsupported
		}
		if f.Parts[0] == "self" {
			return install.Plan{Ops: []install.Op{{Kind: install.PutSelf, Dst: filepath.Join(loc.ModsDir, f.Folder)}}}, nil
		}
		// Script mods work at most one folder deep, so every download gets
		// one folder with its files side by side.
		var ops []install.Op
		for _, s := range f.Parts[1:] {
			dst := filepath.Join(loc.ModsDir, f.Folder, path.Base(s))
			if simsTrayExts[strings.ToLower(path.Ext(s))] {
				dst = filepath.Join(loc.Root, "Tray", path.Base(s))
			}
			ops = append(ops, install.Op{Kind: install.PutFile, Src: s, Dst: dst})
		}
		return install.Plan{Ops: ops}, nil
	}
	return p
}

// simsFolder finds the game's folder below "Documents/Electronic Arts".
// Its name depends on the game's language ("Die Sims 4"), so the one the
// game used last wins.
func simsFolder(ea string) string {
	best, newest := filepath.Join(ea, "The Sims 4"), int64(0)
	ents, _ := os.ReadDir(ea)
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(ea, e.Name())
		for _, f := range []string{"Options.ini", "Resource.cfg", filepath.Join("Mods", "Resource.cfg")} {
			if st, err := os.Stat(filepath.Join(dir, f)); err == nil && st.ModTime().UnixNano() > newest {
				best, newest = dir, st.ModTime().UnixNano()
			}
		}
	}
	return best
}

// ---------- Cyberpunk 2077 ----------

// cpTops are the game folders Cyberpunk 2077 mods put files into. REDmod
// mods (mods/) need the game's own deploy step and are not installed.
var cpTops = []string{"archive", "bin", "engine", "r6", "red4ext"}

// cpPaths are spelled in lower case, as in the game, whatever the archive
// says, because Linux tells "R6" and "r6" apart.
var cpPaths = []string{"archive/pc/mod", "bin/x64/plugins/cyber_engine_tweaks/mods", "bin/x64/plugins", "bin/x64",
	"r6/scripts", "r6/tweaks", "r6/config", "red4ext/plugins", "engine/config", "engine/tools", "archive", "bin", "engine", "r6", "red4ext"}

func cyberpunkProfile() *profile {
	p := &profile{appID: 1091500, name: "Cyberpunk 2077", tracked: true}
	p.analyze = func(src archive.Source, entries []archive.Entry) []Found {
		var files []string
		for _, e := range entries {
			if !e.Dir && !archive.Ignored(e.Name) {
				files = append(files, e.Name)
			}
		}
		if len(files) == 0 {
			return nil
		}
		base, version := downloadName(src.Path())
		root, kids := contentRoot(files)
		folder := base
		if root != "" {
			folder = path.Base(root)
		}
		f := Found{Kind: "mod", Name: folder, ModID: strings.ToLower(SafeFolderName(folder)), Version: version, Folder: SafeFolderName(folder)}
		var tops []string
		for _, t := range cpTops {
			if k := kids[t]; k.dir {
				tops = append(tops, k.name)
			}
		}
		if len(tops) > 0 {
			f.Key = root
			f.Parts = []string{"tree"}
			for _, file := range files {
				rel, ok := strings.CutPrefix(file, join(root, ""))
				if root == "" {
					rel, ok = file, true
				}
				if !ok {
					continue
				}
				top := strings.ToLower(strings.SplitN(rel, "/", 2)[0])
				ext := strings.ToLower(path.Ext(file))
				if !containsFold(tops, top) {
					continue
				}
				if programExts[ext] && !strings.HasPrefix(strings.ToLower(rel), "engine/tools/") {
					return nil // programs only where redscript keeps its compiler
				}
				f.Parts = append(f.Parts, file)
			}
			return []Found{f}
		}
		if kids["mods"].dir {
			f.Unsupported = "needs_manual"
			return []Found{f}
		}
		// Loose .archive files belong into archive/pc/mod.
		f.Parts = []string{"loose"}
		for _, file := range files {
			switch strings.ToLower(path.Ext(file)) {
			case ".archive", ".xl":
				f.Parts = append(f.Parts, file)
			case ".txt", ".md", ".jpg", ".png", ".pdf":
			default:
				return nil
			}
		}
		if len(f.Parts) == 1 {
			return nil
		}
		return []Found{f}
	}
	p.claim = func(src archive.Source, entries []archive.Entry) bool {
		for _, e := range entries {
			l := strings.ToLower(e.Name)
			if strings.Contains(l, "archive/pc/mod/") || strings.HasPrefix(l, "r6/") || strings.Contains(l, "/r6/") ||
				strings.HasPrefix(l, "red4ext/") || strings.Contains(l, "/red4ext/") || path.Ext(l) == ".archive" {
				return true
			}
		}
		return false
	}
	p.locate = func(env *Env, g *Generic) Location {
		dir := g.app.Dir
		loc := g.baseLocation("cyberpunk", filepath.Join(dir, "archive", "pc", "mod"))
		// Archives put there by hand are listed as well.
		g.lists = []modList{{dir: loc.ModsDir, disabled: filepath.Join(dir, "archive", "pc", "mod_disabled"), files: []string{".archive", ".xl"}}}
		cet := exists(filepath.Join(dir, "bin", "x64", "plugins", "cyber_engine_tweaks.asi"))
		red4 := exists(filepath.Join(dir, "red4ext", "RED4ext.dll"))
		if (cet || red4) && g.lay.proton() {
			loc.Notes = append(loc.Notes, protonOverride("cyberpunk"))
		}
		return loc
	}
	p.status = func(loc Location, inst []Installed, f Found, m *install.Manager) (string, string, []Note) {
		if f.Unsupported != "" {
			return StatusUnsupported, "", []Note{note("warn", f.Unsupported)}
		}
		for _, it := range inst {
			if it.ModID == f.ModID {
				return versionStatus(it.Version, f.Version), it.Version, nil
			}
		}
		return StatusNew, "", nil
	}
	p.plan = func(loc Location, inst []Installed, f Found) (install.Plan, error) {
		if len(f.Parts) < 2 || f.Unsupported != "" {
			return install.Plan{}, errUnsupported
		}
		var ops []install.Op
		for _, file := range f.Parts[1:] {
			var rel string
			if f.Parts[0] == "loose" {
				rel = "archive/pc/mod/" + path.Base(file)
			} else {
				rel = strings.TrimPrefix(file, join(f.Key, ""))
				if f.Key == "" {
					rel = file
				}
				rel = cpCase(rel)
			}
			ops = append(ops, install.Op{Kind: install.PutFile, Src: file, Dst: filepath.Join(loc.GameDir, filepath.FromSlash(rel))})
		}
		return install.Plan{Ops: ops}, nil
	}
	return p
}

// cpCase spells the known game folders of a path in lower case.
func cpCase(rel string) string {
	l := strings.ToLower(rel)
	for _, p := range cpPaths {
		if strings.HasPrefix(l, p+"/") {
			return p + rel[len(p):]
		}
	}
	return rel
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
