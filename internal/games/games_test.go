package games

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/steam"
)

func zipBytes(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func analyze(t *testing.T, g Game, name string, files map[string]string) []Found {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	os.WriteFile(p, zipBytes(files), 0o644)
	src, err := archive.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ents, err := src.Entries()
	if err != nil {
		t.Fatal(err)
	}
	out := g.Analyze(src, ents)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.2", "1.10", -1}, {"1.21", "1.21.0", 0}, {"1.21.1", "1.21", 1}, {"v1.3", "1.3", 0},
		{"1.2-beta", "1.2", -1}, {"2.0", "1.9.9", 1}, {"0.6.13+mc1.21.1", "0.5.11+mc1.21.1", 1},
		{"26.1.2", "1.21.10", 1},
	} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestVersionFromName(t *testing.T) {
	for in, want := range map[string]string{
		"Seamless Co-op v1.8.6": "1.8.6", "NeuesHUD_v1.3": "1.3", "Elden Mod Loader 1.4": "1.4",
		"SchnellerRollen": "", "Mod-1-2-3": "",
	} {
		if got := VersionFromName(in); got != want {
			t.Errorf("VersionFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatchMC(t *testing.T) {
	for _, c := range []struct {
		req, v string
		ok     bool
	}{
		{"~1.21.1", "1.21.1", true}, {"~1.21.1", "1.21.4", true}, {"~1.21.1", "1.22", false}, {"~1.21.1", "1.21", false},
		{">=1.21 <1.21.5", "1.21.4", true}, {">=1.21 <1.21.5", "1.21.5", false},
		{"1.21.x", "1.21.3", true}, {"1.21.x", "1.20.6", false}, {"1.20.1 || 1.21.1", "1.21.1", true},
		{"[1.20.1,1.21)", "1.20.4", true}, {"[1.20.1,1.21)", "1.21", false}, {"[1.21.1]", "1.21.1", true},
		{"[1.19,1.20),[1.20.1,)", "1.20.1", true}, {"*", "1.0", true}, {"", "1.0", true},
		{"1.21.1", "1.21.1", true}, {"1.21.1", "1.21.2", false}, {">=26.1", "26.1.2", true},
	} {
		if ok, _ := MatchMC(c.req, c.v); ok != c.ok {
			t.Errorf("MatchMC(%q, %q) = %v, want %v", c.req, c.v, ok, c.ok)
		}
	}
}

func TestParseModInfoBothFormats(t *testing.T) {
	v2 := ParseModInfo([]byte("\xef\xbb\xbf<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<xml>\n<Name value=\"HUD\"/>\n<DisplayName value=\"Neues HUD\"/>\n<Version value=\"1.3\"/>\n</xml>"))
	if v2 != (ModInfo{Name: "HUD", DisplayName: "Neues HUD", Version: "1.3"}) {
		t.Errorf("v2 = %+v", v2)
	}
	v1 := ParseModInfo([]byte(`<ModInfo><ModInfo><Name value="Old"/><Description value="d"/><Version value="0.1"/></ModInfo></ModInfo>`))
	if v1.Name != "Old" || v1.Version != "0.1" {
		t.Errorf("v1 = %+v", v1)
	}
}

func TestSevenDaysAnalyzeAllCases(t *testing.T) {
	g := &SevenDays{}
	mi := func(name, v string) string {
		return `<xml><Name value="` + name + `"/><Version value="` + v + `"/></xml>`
	}
	// Case A: correct folder.
	f := analyze(t, g, "a.zip", map[string]string{"HUD/ModInfo.xml": mi("HUD", "1.3"), "HUD/XUi/x.xml": ""})
	if len(f) != 1 || f[0].Key != "HUD" || f[0].Folder != "HUD" || f[0].Version != "1.3" {
		t.Errorf("case A: %+v", f)
	}
	// Case B: no folder, name from ModInfo.xml.
	f = analyze(t, g, "Rucksack60.zip", map[string]string{"ModInfo.xml": mi("BiggerBackpack60", "1.0"), "Config/a.xml": ""})
	if len(f) != 1 || f[0].Key != "" || f[0].Folder != "BiggerBackpack60" {
		t.Errorf("case B: %+v", f)
	}
	// Case C: double packed, with code.
	f = analyze(t, g, "c.zip", map[string]string{"Pack-v2/Mods/Vehicles/ModInfo.xml": mi("Vehicles", "2"), "Pack-v2/Mods/Vehicles/V.dll": "MZ"})
	if len(f) != 1 || f[0].Key != "Pack-v2/Mods/Vehicles" || f[0].Folder != "Vehicles" || !f[0].HasCode {
		t.Errorf("case C: %+v", f)
	}
	// Case D: several mods; a nested ModInfo.xml inside a mod is ignored.
	f = analyze(t, g, "d.zip", map[string]string{
		"S/Map/ModInfo.xml": mi("Map", "1"), "S/Loot/ModInfo.xml": mi("Loot", "1"), "S/Loot/Extras/ModInfo.xml": mi("X", "1"),
	})
	if len(f) != 2 || f[0].Key != "S/Loot" || f[1].Key != "S/Map" {
		t.Errorf("case D: %+v", f)
	}
	// Not a mod.
	if f := analyze(t, g, "photos.zip", map[string]string{"IMG_1.jpg": ""}); len(f) != 0 {
		t.Errorf("photos: %+v", f)
	}
}

func TestEldenRingAnalyze(t *testing.T) {
	g := &EldenRing{}
	f := analyze(t, g, "Seamless Co-op v1.8.6.zip", map[string]string{
		"SeamlessCoop/ersc.dll": "", "SeamlessCoop/ersc_settings.ini": "", "ersc_launcher.exe": "",
	})
	if len(f) != 1 || f[0].Kind != "coop" || f[0].Version != "1.8.6" || f[0].Parts[0] != "SeamlessCoop" || f[0].Parts[1] != "ersc_launcher.exe" {
		t.Errorf("coop: %+v", f)
	}
	f = analyze(t, g, "wrapped.zip", map[string]string{"Seamless/SeamlessCoop/ersc.dll": "", "Seamless/ersc_launcher.exe": ""})
	if len(f) != 1 || f[0].Parts[0] != "Seamless/SeamlessCoop" || f[0].Parts[1] != "Seamless/ersc_launcher.exe" {
		t.Errorf("wrapped coop: %+v", f)
	}
	f = analyze(t, g, "loader.zip", map[string]string{"dinput8.dll": "", "mod_loader_config.ini": ""})
	if len(f) != 1 || f[0].Kind != "loader" {
		t.Errorf("loader: %+v", f)
	}
	f = analyze(t, g, "roll.zip", map[string]string{"Roll/mods/Roll.dll": "MZ", "Roll/mods/Roll.ini": ""})
	if len(f) != 1 || f[0].Kind != "dllmod" || f[0].Key != "Roll/mods" || f[0].Name != "Roll" {
		t.Errorf("dll mod: %+v", f)
	}
	f = analyze(t, g, "Elden Ring Reforged.zip", map[string]string{"regulation.bin": "", "parts/a.dcx": ""})
	if len(f) != 1 || f[0].Unsupported != "needs_modengine" {
		t.Errorf("me2: %+v", f)
	}
	// A 7 Days to Die mod with a DLL must not look like an Elden Ring mod.
	if f := analyze(t, g, "7dtd.zip", map[string]string{"M/ModInfo.xml": "<xml/>", "M/M.dll": ""}); len(f) != 0 {
		t.Errorf("foreign mod detected: %+v", f)
	}
}

func TestMinecraftJarMeta(t *testing.T) {
	fabric, ok := ParseJarBytes(zipBytes(map[string]string{"fabric.mod.json": `{"id":"sodium","name":"Sodium","version":"0.6.0","depends":{"minecraft":["1.21","1.21.1"],"fabric-api":"*","fabricloader":">=0.15"}}`}))
	if !ok || fabric.Loader != "fabric" || fabric.MC != "1.21 || 1.21.1" || len(fabric.Deps) != 1 || fabric.Deps[0] != "fabric-api" {
		t.Errorf("fabric: %+v", fabric)
	}
	// Line breaks inside strings are accepted, like the Fabric loader does.
	lenient, ok := ParseJarBytes(zipBytes(map[string]string{"fabric.mod.json": "{\"id\":\"x\",\"description\":\"two\nlines\"}"}))
	if !ok || lenient.ID != "x" {
		t.Errorf("lenient: %+v", lenient)
	}
	quilt, ok := ParseJarBytes(zipBytes(map[string]string{"quilt.mod.json": `{"quilt_loader":{"id":"q","version":"1","metadata":{"name":"Q"},"depends":[{"id":"minecraft","versions":">=1.20"},"quilted_fabric_api"]}}`}))
	if !ok || quilt.Loader != "quilt" || quilt.MC != ">=1.20" || quilt.Name != "Q" {
		t.Errorf("quilt: %+v", quilt)
	}
	forge, ok := ParseJarBytes(zipBytes(map[string]string{
		"META-INF/mods.toml":   "modLoader=\"javafml\"\n[[mods]]\nmodId=\"jei\"\nversion=\"${file.jarVersion}\"\ndisplayName=\"JEI\"\n[[dependencies.jei]]\nmodId=\"minecraft\"\nversionRange=\"[1.20.1,1.20.2)\"\n",
		"META-INF/MANIFEST.MF": "Manifest-Version: 1.0\nImplementation-Version: 15.2.0\n",
	}))
	if !ok || forge.Loader != "forge" || forge.Version != "15.2.0" || forge.MC != "[1.20.1,1.20.2)" {
		t.Errorf("forge: %+v", forge)
	}
	neo, ok := ParseJarBytes(zipBytes(map[string]string{"META-INF/neoforge.mods.toml": "[[mods]]\nmodId=\"create\"\nversion=\"6.0\"\n"}))
	if !ok || neo.Loader != "neoforge" {
		t.Errorf("neoforge: %+v", neo)
	}
	if _, ok := ParseJarBytes(zipBytes(map[string]string{"a.class": ""})); ok {
		t.Error("library jar detected as mod")
	}
}

func TestMinecraftAnalyzePacks(t *testing.T) {
	g := &Minecraft{}
	f := analyze(t, g, "Faithful.zip", map[string]string{"pack.mcmeta": "{}", "assets/minecraft/x.png": ""})
	if len(f) != 1 || f[0].Kind != "resourcepack" || len(f[0].Parts) != 1 || f[0].Parts[0] != "self" {
		t.Errorf("resource pack: %+v", f)
	}
	f = analyze(t, g, "Shader.zip", map[string]string{"BSL/shaders/final.fsh": ""})
	if len(f) != 1 || f[0].Kind != "shaderpack" || f[0].Key != "BSL" {
		t.Errorf("shader pack: %+v", f)
	}
	f = analyze(t, g, "pack.zip", map[string]string{"modrinth.index.json": "{}"})
	if len(f) != 1 || f[0].Unsupported != "mc_modpack" {
		t.Errorf("modpack: %+v", f)
	}
	inner := string(zipBytes(map[string]string{"fabric.mod.json": `{"id":"a","version":"1"}`}))
	f = analyze(t, g, "jars.zip", map[string]string{"mods/a-1.jar": inner, "readme.txt": ""})
	if len(f) != 1 || f[0].Key != "mods/a-1.jar" || f[0].Folder != "a-1.jar" {
		t.Errorf("jar in zip: %+v", f)
	}
}

func TestSetupFabric(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path)
		switch r.URL.Path {
		case "/v2/versions/game":
			w.Write([]byte(`[{"version":"26.1","stable":true},{"version":"1.21.2","stable":true},{"version":"1.21.1","stable":true},{"version":"1.21.1-rc1","stable":false}]`))
		case "/v2/versions/loader/1.21.1":
			w.Write([]byte(`[{"loader":{"version":"0.17.0-beta","stable":false}},{"loader":{"version":"0.16.14","stable":true}}]`))
		case "/v2/versions/loader/1.21.1/0.16.14/profile/json":
			w.Write([]byte(`{"id":"fabric-loader-0.16.14-1.21.1","inheritsFrom":"1.21.1","libraries":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("ANYINSTALL_FABRIC_META", srv.URL)

	mc := t.TempDir()
	loc := Location{Found: true, Root: mc, ModsDir: filepath.Join(mc, "mods")}
	g := &Minecraft{}
	if _, err := g.SetupFabric(loc, srv.Client(), "", ""); err != ErrLauncherNotRun {
		t.Fatalf("without launcher profiles: %v", err)
	}
	os.WriteFile(filepath.Join(mc, "launcher_profiles.json"), []byte(`{"profiles":{"x":{"name":"Latest"}},"settings":{"keep":true}}`), 0o644)
	res, err := g.SetupFabric(loc, srv.Client(), "~1.21.1", "")
	if err != nil {
		t.Fatal(err)
	}
	if res.MC != "1.21.1" || res.Loader != "0.16.14" || res.Profile != "fabric-loader-1.21.1" {
		t.Fatalf("result = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(mc, "versions", "fabric-loader-0.16.14-1.21.1", "fabric-loader-0.16.14-1.21.1.json")); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(mc, "launcher_profiles.json"))
	var lp map[string]any
	json.Unmarshal(b, &lp)
	profiles := lp["profiles"].(map[string]any)
	if profiles["x"] == nil || lp["settings"] == nil {
		t.Error("existing launcher settings were lost")
	}
	if p := profiles["fabric-loader-1.21.1"].(map[string]any); p["lastVersionId"] != "fabric-loader-0.16.14-1.21.1" {
		t.Errorf("profile = %v", p)
	}
	if got := Profiles(mc); len(got) != 1 || got[0].Loader != "fabric" || got[0].MC != "1.21.1" {
		t.Errorf("Profiles = %+v", got)
	}
	if !strings.Contains(strings.Join(requests, ","), "/v2/versions/game") {
		t.Error("game versions not queried")
	}
}

func TestEldenRingIgnoresOtherDLLDownloads(t *testing.T) {
	g := &EldenRing{}
	// SMAPI, the Stardew Valley mod loader installer: many DLLs plus scripts.
	smapi := map[string]string{
		"SMAPI 4.5.2 installer/install on Windows.bat":                     "",
		"SMAPI 4.5.2 installer/install on Linux.sh":                        "",
		"SMAPI 4.5.2 installer/internal/windows/SMAPI.Installer.dll":       "",
		"SMAPI 4.5.2 installer/internal/windows/SMAPI.Toolkit.dll":         "",
		"SMAPI 4.5.2 installer/internal/windows/SMAPI.Installer.deps.json": "",
	}
	if f := analyze(t, g, "SMAPI-4.5.2-installer.zip", smapi); len(f) != 0 {
		t.Errorf("SMAPI detected as Elden Ring mod: %+v", f)
	}
	// A lone DLL without any sign that it belongs to Elden Ring.
	if f := analyze(t, g, "SomeTool.zip", map[string]string{"SomeTool.dll": "MZ", "readme.txt": ""}); len(f) != 0 {
		t.Errorf("lone DLL detected: %+v", f)
	}
	// Folders named like game folders are not enough for Mod Engine 2.
	if f := analyze(t, g, "website.zip", map[string]string{"font/a.ttf": "", "script/app.js": "", "asset/logo.png": ""}); len(f) != 0 {
		t.Errorf("website detected as Mod Engine 2 mod: %+v", f)
	}
	// Real signs still work: DLL with its config, FromSoftware file types.
	if f := analyze(t, g, "FasterRoll.zip", map[string]string{"FasterRoll.dll": "MZ", "FasterRoll.ini": ""}); len(f) != 1 || f[0].Kind != "dllmod" {
		t.Errorf("DLL with config: %+v", f)
	}
	if f := analyze(t, g, "EldenRing_NoHUD.zip", map[string]string{"NoHUD.dll": "MZ"}); len(f) != 1 || f[0].Kind != "dllmod" {
		t.Errorf("DLL in download named after the game: %+v", f)
	}
	// A DLL in a "mods" folder alone is common in other games too.
	if f := analyze(t, g, "HollowMod.zip", map[string]string{"Mods/HollowMod.dll": "MZ"}); len(f) != 0 {
		t.Errorf("other game's mods folder detected: %+v", f)
	}
	// FromSoftware files only count when the download is about Elden Ring.
	if f := analyze(t, g, "Elden Ring Armor.zip", map[string]string{"parts/am_m_1000.partsbnd.dcx": ""}); len(f) != 1 || f[0].Kind != "me2" {
		t.Errorf("Mod Engine 2 files: %+v", f)
	}
	if f := analyze(t, g, "DS3 Armor.zip", map[string]string{"parts/am_m_1000.partsbnd.dcx": ""}); len(f) != 0 {
		t.Errorf("Dark Souls mod detected as Elden Ring: %+v", f)
	}
	if f := analyze(t, g, "Reforged (1).zip", map[string]string{"config_eldenring.toml": "", "regulation.bin": ""}); len(f) != 1 || f[0].ModID != "reforged" {
		t.Errorf("duplicate download suffix kept in id: %+v", f)
	}
}

func TestSevenDaysPackSkipsGameMods(t *testing.T) {
	g := &SevenDays{}
	mi := func(name string) string { return `<xml><Name value="` + name + `"/><Version value="1.0"/></xml>` }
	f := analyze(t, g, "Overhaul-Launcher.zip", map[string]string{
		"Game/Mods/0_TFP_Harmony/ModInfo.xml": mi("0_TFP_Harmony"),
		"Game/Mods/0-SCore/ModInfo.xml":       mi("0-SCore"),
		"Game/Mods/BdubsVehicles/ModInfo.xml": mi("BdubsVehicles"),
	})
	if len(f) != 2 || f[0].ModID != "0-SCore" || f[1].ModID != "BdubsVehicles" {
		t.Errorf("pack mods = %+v, want 0-SCore and BdubsVehicles without the game's Harmony", f)
	}
	for _, m := range f {
		if len(m.Notes) != 1 || m.Notes[0].Code != "game_copy" {
			t.Errorf("%s: notes = %+v, want the game copy hint", m.ModID, m.Notes)
		}
	}
	f = analyze(t, g, "Launcher-Copy.zip", map[string]string{
		"7 Days To Die/7DaysToDie.exe":           "MZ",
		"7 Days To Die/Mods/0-SCore/ModInfo.xml": mi("0-SCore"),
	})
	if len(f) != 1 || len(f[0].Notes) != 1 || f[0].Notes[0].Code != "game_copy" {
		t.Errorf("game copy without TFP mods = %+v", f)
	}
	f = analyze(t, g, "Modpack.zip", map[string]string{
		"Mods/0-SCore/ModInfo.xml":       mi("0-SCore"),
		"Mods/BdubsVehicles/ModInfo.xml": mi("BdubsVehicles"),
	})
	if len(f) != 2 || f[0].Notes != nil || f[1].Notes != nil {
		t.Errorf("a plain pack has no game copy hint: %+v", f)
	}
}

// sevenDaysWorld builds a home folder with Steam and 7 Days to Die in the
// given library ("" = the Steam folder itself).
func sevenDaysWorld(t *testing.T, lib string) (home string, env *Env) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Proton only exists on Linux")
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("ANYINSTALL_7DTD_USERDATA", "")
	root := filepath.Join(home, ".local", "share", "Steam")
	libs := []string{root}
	if lib != "" {
		lib = filepath.Join(home, lib)
		libs = append(libs, lib)
	} else {
		lib = root
	}
	os.MkdirAll(filepath.Join(root, "steamapps"), 0o755)
	os.MkdirAll(filepath.Join(lib, "steamapps", "common", "7 Days To Die"), 0o755)
	os.WriteFile(filepath.Join(lib, "steamapps", "common", "7 Days To Die", "7DaysToDie.x86_64"), nil, 0o755)
	os.WriteFile(filepath.Join(lib, "steamapps", "appmanifest_251570.acf"),
		[]byte(`"AppState" { "appid" "251570" "name" "7 Days to Die" "installdir" "7 Days To Die" }`), 0o644)
	return home, &Env{Steam: &steam.Steam{Root: root, Libraries: libs}}
}

func setCompatTool(t *testing.T, env *Env, name string) {
	t.Helper()
	dir := filepath.Join(env.Steam.Root, "config")
	os.MkdirAll(dir, 0o755)
	vdf := `"InstallConfigStore" { "Software" { "Valve" { "Steam" { "CompatToolMapping" { "251570" { "name" "` + name + `" } } } } } }`
	if err := os.WriteFile(filepath.Join(dir, "config.vdf"), []byte(vdf), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSevenDaysUserDataNativeAndProton(t *testing.T) {
	home, env := sevenDaysWorld(t, "Games")
	g := &SevenDays{}
	native := filepath.Join(home, ".local", "share", "7DaysToDie")
	pfx := filepath.Join(home, "Games", "steamapps", "compatdata", "251570", "pfx")
	inPfx := filepath.Join(pfx, "drive_c", "users", "steamuser", "AppData", "Roaming", "7DaysToDie")
	mod := Found{Key: "HUD", Kind: "mod", ModID: "HUD", Folder: "HUD"}

	loc := g.Locate(env)
	if !loc.Found || loc.ModsDir != filepath.Join(native, "Mods") || loc.Proton {
		t.Fatalf("nothing set: %+v", loc)
	}

	// Proton forced in Steam, but the game has not been started with it.
	setCompatTool(t, env, "proton_9")
	loc = g.Locate(env)
	if loc.ModsDir != filepath.Join(inPfx, "Mods") || !loc.Proton || len(loc.Notes) != 1 || loc.Notes[0].Code != "proton_no_prefix" {
		t.Fatalf("proton without prefix: %+v", loc)
	}
	if _, err := g.Plan(loc, nil, mod); !errors.Is(err, ErrStartGameFirst) {
		t.Errorf("install before the first start: err = %v", err)
	}
	// Opening the Mods folder creates it, but Proton has still not set
	// up its prefix.
	os.MkdirAll(filepath.Join(inPfx, "Mods"), 0o755)
	if loc = g.Locate(env); len(loc.Notes) != 1 || Ready(loc) == nil {
		t.Errorf("folders alone are not a Proton prefix: %+v", loc)
	}
	if _, err := g.Plan(loc, nil, mod); !errors.Is(err, ErrStartGameFirst) {
		t.Errorf("install into folders without a prefix: err = %v", err)
	}
	os.WriteFile(filepath.Join(pfx, "system.reg"), nil, 0o644)
	loc = g.Locate(env)
	if loc.ModsDir != filepath.Join(inPfx, "Mods") || !loc.Proton || len(loc.Notes) != 0 {
		t.Fatalf("proton: %+v", loc)
	}
	if p, err := g.Plan(loc, nil, mod); err != nil || p.Ops[0].Dst != filepath.Join(inPfx, "Mods", "HUD") {
		t.Errorf("plan = %+v, %v", p, err)
	}

	// The Steam Linux Runtime runs the Linux version.
	setCompatTool(t, env, "steamlinuxruntime_sniper")
	if loc = g.Locate(env); loc.ModsDir != filepath.Join(native, "Mods") || loc.Proton {
		t.Errorf("linux runtime: %+v", loc)
	}

	// Nothing set: the folder the game wrote to last wins.
	os.Remove(filepath.Join(env.Steam.Root, "config", "config.vdf"))
	if loc = g.Locate(env); loc.ModsDir != filepath.Join(inPfx, "Mods") {
		t.Errorf("only the prefix was used: %+v", loc)
	}
	old, recent := time.Now().Add(-48*time.Hour), time.Now().Add(-time.Hour)
	for _, d := range []string{filepath.Join(native, "logs"), filepath.Join(inPfx, "logs")} {
		os.MkdirAll(d, 0o755)
	}
	os.Chtimes(filepath.Join(native, "logs"), recent, recent)
	os.Chtimes(filepath.Join(inPfx, "logs"), old, old)
	// Any Install's own folders do not count as the game being used.
	os.MkdirAll(filepath.Join(inPfx, "Mods"), 0o755)
	if loc = g.Locate(env); loc.ModsDir != filepath.Join(native, "Mods") || loc.Proton {
		t.Errorf("linux version used last: %+v", loc)
	}
	// Playing only changes a file deep inside the saves.
	save := filepath.Join(inPfx, "Saves", "Navezgane", "MyGame")
	os.MkdirAll(save, 0o755)
	os.WriteFile(filepath.Join(save, "main.ttw"), nil, 0o644)
	for _, d := range []string{filepath.Join(inPfx, "Saves"), filepath.Join(inPfx, "Saves", "Navezgane"), save, filepath.Join(save, "main.ttw")} {
		os.Chtimes(d, old, old)
	}
	if loc = g.Locate(env); loc.Proton {
		t.Errorf("old saves in the prefix: %+v", loc)
	}
	os.Chtimes(filepath.Join(save, "main.ttw"), time.Now(), time.Now())
	if loc = g.Locate(env); loc.ModsDir != filepath.Join(inPfx, "Mods") || !loc.Proton {
		t.Errorf("proton used last: %+v", loc)
	}
}

func TestSevenDaysProtonPrefixInGameLibrary(t *testing.T) {
	home, env := sevenDaysWorld(t, "Games")
	setCompatTool(t, env, "proton_9")
	// A folder chosen by hand that is not inside a Steam library: the
	// prefix is still the one in the library Steam runs the game from.
	custom := filepath.Join(home, "Spiele", "7DTD")
	os.MkdirAll(custom, 0o755)
	os.WriteFile(filepath.Join(custom, "7DaysToDie.x86_64"), nil, 0o755)
	env.Custom = map[string]string{"7dtd": custom}
	loc := (&SevenDays{}).Locate(env)
	want := filepath.Join(home, "Games", "steamapps", "compatdata", "251570", "pfx", "drive_c", "users", "steamuser", "AppData", "Roaming", "7DaysToDie", "Mods")
	if loc.ModsDir != want {
		t.Errorf("mods dir = %s, want %s", loc.ModsDir, want)
	}
}

func TestSevenDaysNativeUserDataSandbox(t *testing.T) {
	home, env := sevenDaysWorld(t, "")
	sandbox := filepath.Join(home, ".var", "app", "com.valvesoftware.Steam")
	env.Steam.Root = filepath.Join(sandbox, ".local", "share", "Steam")
	if got, want := nativeUserData(env), filepath.Join(sandbox, ".local", "share", "7DaysToDie"); got != want {
		t.Errorf("flatpak: %s, want %s", got, want)
	}
	env.Steam.Root = filepath.Join(home, ".local", "share", "Steam")
	xdg := filepath.Join(home, "data")
	t.Setenv("XDG_DATA_HOME", xdg)
	if got := nativeUserData(env); got != filepath.Join(xdg, "7DaysToDie") {
		t.Errorf("XDG_DATA_HOME before the first start: %s", got)
	}
	os.MkdirAll(filepath.Join(home, ".local", "share", "7DaysToDie"), 0o755)
	if got := nativeUserData(env); got != filepath.Join(home, ".local", "share", "7DaysToDie") {
		t.Errorf("game that ignores XDG_DATA_HOME: %s", got)
	}
}

func TestSevenDaysLinuxTip(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux only")
	}
	g := &SevenDays{}
	code := []Found{{Name: "SCore", HasCode: true}}
	has := func(tips []Note) bool {
		for _, n := range tips {
			if n.Code == "tip_7dtd_linux_proton" {
				return true
			}
		}
		return false
	}
	if !has(g.Tips(Location{}, code)) {
		t.Error("native Linux with a code mod should suggest Proton")
	}
	if has(g.Tips(Location{Proton: true}, code)) || has(g.Tips(Location{}, []Found{{Name: "HUD"}})) {
		t.Error("no Proton hint with Proton or without code mods")
	}
}
