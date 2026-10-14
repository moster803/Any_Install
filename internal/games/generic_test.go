package games

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/steam"
)

// touch creates files (content "") below root.
func touch(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(root, filepath.FromSlash(n))
		if strings.HasSuffix(n, "/") {
			os.MkdirAll(p, 0o755)
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInspectFindsModSystems(t *testing.T) {
	cases := []struct {
		name   string
		files  []string
		system string
		mods   string
		anti   bool
	}{
		{"bepinex", []string{"Game.exe", "winhttp.dll", "BepInEx/core/BepInEx.dll", "Game_Data/Managed/Assembly-CSharp.dll"}, SysBepInEx, "BepInEx/plugins", false},
		{"melon", []string{"Game.exe", "version.dll", "MelonLoader/net35/MelonLoader.dll", "Mods/"}, SysMelon, "Mods", false},
		{"unreal", []string{"Pal.exe", "Engine/Binaries/x.dll", "Pal/Content/Paks/Pal-Windows.pak", "Pal/Binaries/Win64/Pal-Win64-Shipping.exe"}, SysUnreal, "Pal/Content/Paks/~mods", false},
		{"unreal-eac", []string{"Game.exe", "EasyAntiCheat/Settings.json", "Game/Content/Paks/Game.pak"}, SysUnreal, "Game/Content/Paks/~mods", true},
		{"modsdir", []string{"RimWorldWin64.exe", "Mods/Place mods here.txt"}, SysModsDir, "Mods", false},
		{"lowercase mods", []string{"kenshi_x64.exe", "mods/"}, SysModsDir, "mods", false},
		{"nothing", []string{"portal2.exe", "portal2/pak01_dir.vpk"}, "", "", false},
		{"bepinex folder without core", []string{"Game.exe", "BepInEx/plugins/"}, "", "", false},
	}
	for _, c := range cases {
		dir := t.TempDir()
		touch(t, dir, c.files...)
		l := inspect(dir)
		if l.system != c.system || l.antiCheat != c.anti {
			t.Errorf("%s: system %q anti %v, want %q %v", c.name, l.system, l.antiCheat, c.system, c.anti)
		}
		if c.mods != "" && l.modsDir != filepath.Join(dir, filepath.FromSlash(c.mods)) {
			t.Errorf("%s: mods %s", c.name, l.modsDir)
		}
	}
	dir := t.TempDir()
	touch(t, dir, "Game/Content/Paks/Game.pak", "Game/Content/Paks/Game.sig")
	if l := inspect(dir); !l.signed {
		t.Error("signed paks not seen")
	}
}

func kinds(fs []Found) []string {
	var out []string
	for _, f := range fs {
		k := f.Kind
		if len(f.Parts) > 0 {
			k += ":" + f.Parts[0]
			if len(f.Parts) > 1 && (f.Parts[0] != typePak) {
				k += ":" + f.Parts[1]
			}
		}
		if f.Unsupported != "" {
			k += "!" + f.Unsupported
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestAnalyzeGenericDownloads(t *testing.T) {
	g := NewGeneric(4000) // a game without profile
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"BepInExPack.zip", map[string]string{"manifest.json": `{"name":"BepInExPack","version_number":"5.4.2100"}`, "icon.png": "",
			"BepInExPack/winhttp.dll": "MZ", "BepInExPack/doorstop_config.ini": "", "BepInExPack/BepInEx/core/BepInEx.Preloader.dll": "MZ"}, "loader:loader:bepinex"},
		{"MelonLoader.x64.zip", map[string]string{"version.dll": "MZ", "MelonLoader/net6/MelonLoader.dll": "MZ"}, "loader:loader:melonloader"},
		{"Author-MoreCompany-1.9.1.zip", map[string]string{"manifest.json": `{"name":"MoreCompany","version_number":"1.9.1","dependencies":["BepInEx-BepInExPack-5.4.2100"]}`,
			"icon.png": "", "README.md": "", "MoreCompany.dll": "MZ BepInEx"}, "mod:thunderstore:bepinex"},
		{"SomePlugin.zip", map[string]string{"SomePlugin/SomePlugin.dll": "MZ...BepInEx.BaseUnityPlugin", "SomePlugin/assets/x.bundle": ""}, "mod:dll:bepinex"},
		{"TreePlugin.zip", map[string]string{"BepInEx/plugins/Tree/Tree.dll": "MZ", "BepInEx/config/tree.cfg": ""}, "mod:tree:bepinex"},
		{"MelonMod.zip", map[string]string{"Mods/Fast.dll": "MZ MelonLoader", "UserLibs/Lib.dll": "MZ"}, "mod:tree:melonloader"},
		{"Unity Mod Wrapper.zip", map[string]string{"Wrapper/Mods/A/A.mod": "", "Wrapper/Mods/B/B.mod": ""}, "mod:folder,mod:folder"},
		{"BetterTextures-1.2.zip", map[string]string{"BetterTextures/BetterTextures_P.pak": "", "BetterTextures/BetterTextures_P.utoc": "", "BetterTextures/BetterTextures_P.ucas": ""}, "mod:pak"},
		{"Options.zip", map[string]string{"Option A/A_P.pak": "", "Option B/B_P.pak": ""}, "mod:pak,mod:pak"},
		{"LogicMod.zip", map[string]string{"LogicMods/X.pak": ""}, "mod:pak!needs_manual"},
		{"UE4SS_v3.0.1.zip", map[string]string{"ue4ss/UE4SS.dll": "", "dwmapi.dll": ""}, "loader!needs_manual"},
		{"KenshiMod.zip", map[string]string{"KenshiMod/KenshiMod.mod": "", "KenshiMod/readme.txt": ""}, "mod:folder"},
		{"7dtd.zip", map[string]string{"HUD/ModInfo.xml": "<xml/>", "HUD/x.dll": "MZ"}, ""},
		{"Tool.zip", map[string]string{"Tool/tool.exe": "MZ", "Tool/x.dll": "MZ"}, ""},
		{"Photos.zip", map[string]string{"a.jpg": "", "b.png": "", "info.txt": ""}, ""},
		{"Coop.zip", map[string]string{"SeamlessCoop/ersc.dll": "MZ", "ersc_launcher.exe": "MZ"}, ""},
		{"StardewMod.zip", map[string]string{"LookupAnything/manifest.json": `{"Name":"Lookup Anything","UniqueID":"Pathoschild.LookupAnything","Version":"1.40.0","EntryDll":"LookupAnything.dll"}`, "LookupAnything/LookupAnything.dll": "MZ"}, ""},
		{"Sims.zip", map[string]string{"Hair.package": ""}, ""},
	}
	for _, c := range cases {
		got := strings.Join(kinds(analyze(t, g, c.name, c.files)), ",")
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		for _, f := range analyze(t, g, c.name, c.files) {
			if !f.Unsure {
				t.Errorf("%s: generic downloads must not be ticked", c.name)
			}
		}
	}
}

func TestFits(t *testing.T) {
	g := NewGeneric(4000)
	pak := analyze(t, g, "P.zip", map[string]string{"P_P.pak": ""})[0]
	dll := analyze(t, g, "D.zip", map[string]string{"D.dll": "MZ"})[0]
	melonDLL := analyze(t, g, "M.zip", map[string]string{"M.dll": "MZ MelonLoader"})[0]
	folder := analyze(t, g, "F.zip", map[string]string{"F/f.cfg": ""})[0]
	for _, c := range []struct {
		sys  string
		f    Found
		want bool
	}{
		{SysUnreal, pak, true}, {SysBepInEx, pak, false}, {SysModsDir, pak, false},
		{SysBepInEx, dll, true}, {SysMelon, dll, true}, {SysModsDir, dll, true}, {SysUnreal, dll, false},
		{SysBepInEx, melonDLL, false}, {SysMelon, melonDLL, true},
		{SysModsDir, folder, true}, {SysBepInEx, folder, false},
	} {
		if got := fits(c.sys, c.f); got != c.want {
			t.Errorf("fits(%s, %s %v) = %v", c.sys, c.f.Name, c.f.Parts, got)
		}
	}
}

// installGeneric analyzes a download and installs every mod in it.
func installGeneric(t *testing.T, g *Generic, loc Location, name string, files map[string]string) []Found {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	os.WriteFile(p, zipBytes(files), 0o644)
	src, err := archive.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ents, _ := src.Entries()
	found := g.Analyze(src, ents)
	m := Manager(loc)
	tx, err := m.Begin(g.ID(), "install")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		plan, err := g.Plan(loc, g.Installed(loc, m), f)
		if err != nil {
			t.Fatalf("%s: plan: %v", name, err)
		}
		if err := tx.Apply(src, plan); err != nil {
			t.Fatalf("%s: apply: %v", name, err)
		}
		if err := g.Track(m, f, plan); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit([]string{name}); err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		if f.Version != "" {
			m.SetVersion(f.ModID, f.Version) // as the server does
		}
	}
	return found
}

// steamWorld creates a Steam folder with one game and returns its folder.
func steamWorld(t *testing.T, appID int, name string, files ...string) (*Env, string) {
	t.Helper()
	root := t.TempDir()
	st := filepath.Join(root, "Steam")
	dir := filepath.Join(st, "steamapps", "common", name)
	touch(t, dir, files...)
	os.WriteFile(filepath.Join(st, "steamapps", "appmanifest_"+strconv.Itoa(appID)+".acf"),
		[]byte(`"AppState" { "appid" "`+strconv.Itoa(appID)+`" "name" "`+name+`" "installdir" "`+name+`" }`), 0o644)
	return &Env{Steam: &steam.Steam{Root: st, Libraries: []string{st}}}, dir
}

func exist(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(n))); err != nil {
			t.Errorf("missing %s", n)
		}
	}
}

func TestGenericBepInExInstall(t *testing.T) {
	env, dir := steamWorld(t, 4001, "Some Unity Game", "Game.exe", "winhttp.dll", "BepInEx/core/BepInEx.dll")
	g := NewGeneric(4001)
	loc := g.Locate(env)
	if !loc.Found || loc.System != SysBepInEx {
		t.Fatalf("loc = %+v", loc)
	}
	installGeneric(t, g, loc, "Author-MoreCompany-1.9.1.zip", map[string]string{
		"manifest.json": `{"name":"MoreCompany","version_number":"1.9.1"}`, "icon.png": "", "MoreCompany.dll": "MZ BepInEx",
	})
	installGeneric(t, g, loc, "Configured.zip", map[string]string{
		"manifest.json": `{"name":"Configured","version_number":"1.0.0"}`, "plugins/Configured.dll": "MZ", "config/configured.cfg": "a=1",
	})
	installGeneric(t, g, loc, "Tree.zip", map[string]string{"BepInEx/plugins/Tree/Tree.dll": "MZ"})
	installGeneric(t, g, loc, "Loose.dll.zip", map[string]string{"Loose/Loose.dll": "MZ", "Loose/data/x.json": "{}"})
	exist(t, dir, "BepInEx/plugins/MoreCompany/MoreCompany.dll", "BepInEx/plugins/MoreCompany/manifest.json",
		"BepInEx/plugins/Configured/Configured.dll", "BepInEx/config/configured.cfg",
		"BepInEx/plugins/Tree/Tree.dll", "BepInEx/plugins/Loose/Loose.dll", "BepInEx/plugins/Loose/data/x.json")

	m := Manager(loc)
	inst := g.Installed(loc, m)
	if len(inst) != 4 {
		t.Fatalf("installed = %+v", inst)
	}
	// Switching off moves the plugin out of plugins, where BepInEx would
	// still find it in any sub-folder.
	var mc Installed
	for _, it := range inst {
		if it.Name == "MoreCompany" {
			mc = it
		}
	}
	if err := g.SetEnabled(loc, mc, false); err != nil {
		t.Fatal(err)
	}
	exist(t, dir, "BepInEx/plugins_disabled/MoreCompany/MoreCompany.dll")
	f := analyze(t, g, "Author-MoreCompany-1.9.1.zip", map[string]string{"manifest.json": `{"name":"MoreCompany","version_number":"1.9.1"}`, "MoreCompany.dll": "MZ"})[0]
	if st, _, notes := g.Status(loc, g.Installed(loc, m), f, m); st != StatusInstalled || len(notes) != 1 || notes[0].Code != "installed_disabled" {
		t.Errorf("status of the switched off mod: %s %+v", st, notes)
	}
}

func TestGenericUnrealAndModsDirInstall(t *testing.T) {
	env, dir := steamWorld(t, 4002, "Some Unreal Game", "UGame.exe", "UGame/Content/Paks/UGame-Windows.pak")
	g := NewGeneric(4002)
	loc := g.Locate(env)
	if loc.System != SysUnreal {
		t.Fatalf("loc = %+v", loc)
	}
	installGeneric(t, g, loc, "Better Sky-12-1-0.zip", map[string]string{"Sky/BetterSky_P.pak": "", "Sky/BetterSky_P.utoc": "", "Sky/BetterSky_P.ucas": ""})
	exist(t, dir, "UGame/Content/Paks/~mods/Sky/BetterSky_P.pak", "UGame/Content/Paks/~mods/Sky/BetterSky_P.ucas")
	m := Manager(loc)
	inst := g.Installed(loc, m)
	if len(inst) != 1 {
		t.Fatalf("installed = %+v", inst)
	}
	if err := g.SetEnabled(loc, inst[0], false); err != nil {
		t.Fatal(err)
	}
	// Outside of Paks, where the engine looks for paks in every folder.
	exist(t, dir, "UGame/Content/Paks_disabled/Sky/BetterSky_P.pak")

	env, dir = steamWorld(t, 4003, "Some Game With Mods", "Game.exe", "Mods/")
	g = NewGeneric(4003)
	loc = g.Locate(env)
	installGeneric(t, g, loc, "Cool Mod v1.2.zip", map[string]string{"Cool Mod/data/a.xml": "", "Cool Mod/mod.info": ""})
	installGeneric(t, g, loc, "Loose.zip", map[string]string{"data/b.xml": "", "mod.info": ""})
	exist(t, dir, "Mods/Cool Mod/mod.info", "Mods/Loose/mod.info")
	if inst := g.Installed(loc, Manager(loc)); len(inst) != 2 || inst[0].Version != "1.2" {
		t.Errorf("installed = %+v", inst)
	}
}

func TestDetectListsModdableGames(t *testing.T) {
	root := t.TempDir()
	st := filepath.Join(root, "Steam")
	add := func(id int, name string, files ...string) {
		touch(t, filepath.Join(st, "steamapps", "common", name), files...)
		os.WriteFile(filepath.Join(st, "steamapps", "appmanifest_"+strconv.Itoa(id)+".acf"),
			[]byte(`"AppState" { "appid" "`+strconv.Itoa(id)+`" "name" "`+name+`" "installdir" "`+name+`" }`), 0o644)
	}
	add(1, "Unity Game", "G.exe", "BepInEx/core/BepInEx.dll")
	add(2, "Unreal Game", "U.exe", "U/Content/Paks/U.pak")
	add(3, "Online Shooter", "S.exe", "EasyAntiCheat/x", "S/Content/Paks/S.pak")
	add(4, "Plain Game", "P.exe", "data.vpk")
	add(489830, "The Elder Scrolls V: Skyrim Special Edition", "SkyrimSE.exe", "Data/Skyrim.esm")
	add(892970, "Valheim", "valheim.exe", "valheim_Data/Managed/Assembly-CSharp.dll")
	add(1493710, "Proton Experimental", "proton")
	env := &Env{Steam: &steam.Steam{Root: st, Libraries: []string{st}}}
	var got []string
	for _, gm := range Detect(env) {
		if gen, ok := gm.(*Generic); ok {
			l := gen.Locate(env)
			got = append(got, gm.Name()+"="+l.System+l.Refused)
		}
	}
	sort.Strings(got)
	want := "The Elder Scrolls V: Skyrim Special Edition=refused_loadorder,Unity Game=bepinex,Unreal Game=unreal,Valheim=bepinex"
	if strings.Join(got, ",") != want {
		t.Errorf("detected %v\nwant %s", got, want)
	}
	if ByID("steam-892970") == nil || ByID("steam-251570") != nil || ByID("steam-12x") != nil || ByID("steam-0") != nil {
		t.Error("ByID for Steam games")
	}
}

func TestBepInExProfileWithoutLoader(t *testing.T) {
	env, _ := steamWorld(t, 892970, "Valheim", "valheim.exe", "valheim_Data/Managed/Assembly-CSharp.dll")
	g := NewGeneric(892970)
	loc := g.Locate(env)
	var codes []string
	for _, n := range loc.Notes {
		codes = append(codes, n.Code)
	}
	if loc.System != SysBepInEx || !strings.Contains(strings.Join(codes, ","), "loader_missing") {
		t.Fatalf("loc = %+v", loc)
	}
	m := Manager(loc)
	mod := analyze(t, g, "Valheim Plus.zip", map[string]string{"ValheimPlus/ValheimPlus.dll": "MZ BepInEx"})[0]
	if st, _, notes := g.Status(loc, nil, mod, m); st != StatusNew || len(notes) != 1 || notes[0].Code != "needs_loader" {
		t.Errorf("mod before BepInEx: %s %+v", st, notes)
	}
	pack := analyze(t, g, "denikson-BepInExPack_Valheim-5.4.2202.zip", map[string]string{
		"manifest.json": `{"name":"BepInExPack_Valheim","version_number":"5.4.2202"}`, "BepInExPack_Valheim/winhttp.dll": "MZ",
		"BepInExPack_Valheim/BepInEx/core/BepInEx.Preloader.dll": "MZ", "BepInExPack_Valheim/start_game_bepinex.sh": "#!/bin/sh",
		"BepInExPack_Valheim/unstripped_corlib/x.dll": "MZ",
	})
	if len(pack) != 1 || pack[0].Kind != "loader" {
		t.Fatalf("pack = %+v", pack)
	}
	installGeneric(t, g, loc, "denikson-BepInExPack_Valheim-5.4.2202.zip", map[string]string{
		"BepInExPack_Valheim/winhttp.dll": "MZ", "BepInExPack_Valheim/BepInEx/core/BepInEx.Preloader.dll": "MZ",
	})
	loc = g.Locate(env)
	if st, _, notes := g.Status(loc, nil, mod, m); st != StatusNew || len(notes) != 0 {
		t.Errorf("mod after BepInEx: %s %+v", st, notes)
	}
}

func TestMarkerProfiles(t *testing.T) {
	stardew := NewGeneric(413150)
	f := analyze(t, stardew, "Content Patcher 2.5.zip", map[string]string{
		"ContentPatcher/manifest.json":                   `{"Name":"Content Patcher","UniqueID":"Pathoschild.ContentPatcher","Version":"2.5.0","EntryDll":"ContentPatcher.dll"}`,
		"ContentPatcher/ContentPatcher.dll":              "MZ",
		"ContentPatcher/assets/[CP] Inner/manifest.json": `{"Name":"Inner","UniqueID":"x.inner","Version":"1.0"}`,
	})
	if len(f) != 1 || f[0].ModID != "pathoschild.contentpatcher" || f[0].Folder != "ContentPatcher" || f[0].Version != "2.5.0" || f[0].Unsure {
		t.Errorf("stardew: %+v", f)
	}
	f = analyze(t, stardew, "Bundle.zip", map[string]string{
		"Bundle/[CP] Hats/manifest.json": `{"Name":"Hats","UniqueID":"a.hats","Version":"1.0","ContentPackFor":{"UniqueID":"Pathoschild.ContentPatcher"}}`,
		"Bundle/[JA] Food/manifest.json": `{"Name":"Food","UniqueID":"a.food","Version":{"MajorVersion":1,"MinorVersion":2,"PatchVersion":0}}`,
	})
	if len(f) != 2 || f[1].Version != "1.2.0" && f[0].Version != "1.2.0" {
		t.Errorf("stardew bundle: %+v", f)
	}
	// A Thunderstore manifest has no UniqueID: not a Stardew mod.
	if f := analyze(t, stardew, "Thunder.zip", map[string]string{"manifest.json": `{"name":"X","version_number":"1.0.0"}`, "X.dll": "MZ"}); len(f) != 0 {
		t.Errorf("thunderstore in stardew: %+v", f)
	}

	rim := NewGeneric(294100)
	f = analyze(t, rim, "HugsLib.zip", map[string]string{"About/About.xml": `<ModMetaData><name>HugsLib</name><packageId>UnlimitedHugs.HugsLib</packageId></ModMetaData>`, "Assemblies/HugsLib.dll": "MZ"})
	if len(f) != 1 || f[0].Key != "" || f[0].Folder != "HugsLib" || f[0].ModID != "unlimitedhugs.hugslib" {
		t.Errorf("rimworld: %+v", f)
	}

	bl := NewGeneric(261550)
	f = analyze(t, bl, "Harmony-2228-2-3-3.zip", map[string]string{"Modules/Bannerlord.Harmony/SubModule.xml": `<Module><Id value="Bannerlord.Harmony"/><Name value="Harmony"/><Version value="v2.3.3"/></Module>`})
	if len(f) != 1 || f[0].Folder != "Bannerlord.Harmony" || f[0].Version != "2.3.3" {
		t.Errorf("bannerlord: %+v", f)
	}

	// The games do not take each other's mods, but generic games leave
	// them alone too.
	if f := analyze(t, NewGeneric(4000), "HugsLib.zip", map[string]string{"HugsLib/About/About.xml": `<ModMetaData><packageId>a.b</packageId></ModMetaData>`}); len(f) != 0 {
		t.Errorf("rimworld mod offered for another game: %+v", f)
	}
}

func TestStardewInstall(t *testing.T) {
	env, dir := steamWorld(t, 413150, "Stardew Valley", "Stardew Valley.exe", "StardewModdingAPI.dll", "Mods/")
	g := NewGeneric(413150)
	loc := g.Locate(env)
	if !loc.Found || loc.ModsDir != filepath.Join(dir, "Mods") {
		t.Fatalf("loc = %+v", loc)
	}
	installGeneric(t, g, loc, "LookupAnything.zip", map[string]string{
		"LookupAnything 1.40/LookupAnything/manifest.json":      `{"Name":"Lookup Anything","UniqueID":"Pathoschild.LookupAnything","Version":"1.40.0","EntryDll":"LookupAnything.dll"}`,
		"LookupAnything 1.40/LookupAnything/LookupAnything.dll": "MZ",
	})
	exist(t, dir, "Mods/LookupAnything/manifest.json")
	inst := g.Installed(loc, Manager(loc))
	if len(inst) != 1 || inst[0].ModID != "pathoschild.lookupanything" || inst[0].Version != "1.40.0" || inst[0].Name != "Lookup Anything" {
		t.Errorf("installed = %+v", inst)
	}
}

func TestSCSAndSims(t *testing.T) {
	t.Setenv("ANYINSTALL_DOCUMENTS", t.TempDir())
	ets := NewGeneric(227300)
	f := analyze(t, ets, "Realistic Graphics.zip", map[string]string{"Realistic Graphics v4/realistic_graphics.scs": "", "readme.txt": ""})
	if len(f) != 1 || f[0].Parts[0] != "scs" {
		t.Errorf("scs in zip: %+v", f)
	}
	f = analyze(t, ets, "trailer_mod.zip", map[string]string{"manifest.sii": "SiiNunit {}", "def/x.sii": ""})
	if len(f) != 1 || f[0].Parts[0] != "self" || f[0].Folder != "trailer_mod.zip" {
		t.Errorf("zip mod: %+v", f)
	}
	// The truck games share mods.
	if f := analyze(t, NewGeneric(270880), "Realistic Graphics.zip", map[string]string{"a.scs": ""}); len(f) != 1 {
		t.Errorf("ats does not offer an scs mod: %+v", f)
	}

	sims := NewGeneric(1222670)
	f = analyze(t, sims, "Hair Pack.zip", map[string]string{"Hair Pack/hair.package": "", "Hair Pack/Scripts/mc.ts4script": "", "Hair Pack/lot.trayitem": ""})
	if len(f) != 1 || len(f[0].Parts) != 4 || f[0].Unsure {
		t.Errorf("sims: %+v", f)
	}
}

func TestCyberpunkTrackedInstall(t *testing.T) {
	env, dir := steamWorld(t, 1091500, "Cyberpunk 2077", "bin/x64/Cyberpunk2077.exe", "archive/pc/content/basegame_1.archive", "REDprelauncher.exe")
	g := NewGeneric(1091500)
	loc := g.Locate(env)
	if !loc.Found || loc.System != "cyberpunk" {
		t.Fatalf("loc = %+v", loc)
	}
	found := installGeneric(t, g, loc, "Better Car Handling-1234-1-5.zip", map[string]string{
		"Better Car Handling/R6/Scripts/cars.reds":        "",
		"Better Car Handling/archive/pc/mod/cars.archive": "",
		"Better Car Handling/readme.txt":                  "",
	})
	if len(found) != 1 || found[0].Unsure {
		t.Fatalf("found = %+v", found)
	}
	exist(t, dir, "r6/scripts/cars.reds", "archive/pc/mod/cars.archive")
	installGeneric(t, g, loc, "Loose Archive.zip", map[string]string{"loose.archive": ""})
	exist(t, dir, "archive/pc/mod/loose.archive")
	touch(t, dir, "archive/pc/mod/byhand.archive")

	m := Manager(loc)
	inst := g.Installed(loc, m)
	var names []string
	for _, it := range inst {
		names = append(names, it.Name)
	}
	if strings.Join(names, ",") != "Better Car Handling,byhand,Loose Archive" {
		t.Fatalf("installed = %v", names)
	}
	if err := g.SetEnabled(loc, inst[0], false); err != nil {
		t.Fatal(err)
	}
	exist(t, dir, "AnyInstall-Disabled/better car handling/r6/scripts/cars.reds")
	if _, err := os.Stat(filepath.Join(dir, "r6", "scripts", "cars.reds")); err == nil {
		t.Error("switched off mod still in place")
	}
	inst = g.Installed(loc, m)
	if inst[0].Enabled || len(inst[0].Paths) != 2 {
		t.Errorf("switched off = %+v", inst[0])
	}
	if err := g.SetEnabled(loc, inst[0], true); err != nil {
		t.Fatal(err)
	}
	exist(t, dir, "r6/scripts/cars.reds", "archive/pc/mod/cars.archive")
}

func TestDownloadName(t *testing.T) {
	for in, want := range map[string]string{
		"Realistic Rain 4.2.zip":                 "Realistic Rain|4.2",
		"Better Lumos-1234-1-2-1760000000.zip":   "Better Lumos|1.2",
		"SkyUI_5_2_SE-12604-5-2SE-1572563015.7z": "SkyUI_5_2_SE-12604-5-2SE-1572563015|",
		"Author-MoreCompany-1.9.1.zip":           "Author-MoreCompany|1.9.1",
		"Cool Mod v1.2.zip":                      "Cool Mod|1.2",
		"1.2.zip":                                "1.2|1.2",
		"Plain.zip":                              "Plain|",
	} {
		n, v := downloadName(filepath.Join(t.TempDir(), in))
		if n+"|"+v != want {
			t.Errorf("%s: %s|%s, want %s", in, n, v, want)
		}
	}
}
