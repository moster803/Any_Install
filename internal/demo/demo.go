// Package demo builds a pretend computer with Steam, the three supported
// games, a Minecraft folder and a download folder full of example mods.
// "AnyInstall --demo" uses it, so the program can be tried (and tested)
// without owning the games. Nothing outside the demo folder is touched.
package demo

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Build creates the demo world in root and returns the environment
// variables that point Any Install at it.
func Build(root string) (map[string]string, error) {
	steam := filepath.Join(root, "Steam")
	lib2 := filepath.Join(root, "SteamLibrary")
	userData := filepath.Join(root, "AppData", "7DaysToDie")
	mc := filepath.Join(root, "AppData", ".minecraft")
	dl := filepath.Join(root, "Downloads")
	data := filepath.Join(root, "AnyInstall-Daten")

	vdf := fmt.Sprintf(`"libraryfolders"
{
	"0"
	{
		"path"		%q
		"apps" { "1245620" "0" }
	}
	"1"
	{
		"path"		%q
		"apps" { "251570" "0" }
	}
}
`, steam, lib2)
	// %q escapes backslashes the same way Steam does.
	files := map[string]string{
		filepath.Join(steam, "steamapps", "libraryfolders.vdf"):                                             vdf,
		filepath.Join(steam, "steamapps", "appmanifest_1245620.acf"):                                        manifest(1245620, "ELDEN RING", "ELDEN RING"),
		filepath.Join(steam, "steamapps", "common", "ELDEN RING", "Game", "eldenring.exe"):                  "",
		filepath.Join(lib2, "steamapps", "appmanifest_251570.acf"):                                          manifest(251570, "7 Days to Die", "7 Days To Die"),
		filepath.Join(lib2, "steamapps", "common", "7 Days To Die", "7DaysToDie.exe"):                       "",
		filepath.Join(lib2, "steamapps", "common", "7 Days To Die", "Mods", "0_TFP_Harmony", "ModInfo.xml"): modInfo("0_TFP_Harmony", "Harmony", "2.12.0.0"),

		// Already installed 7 Days to Die mods: an old HUD version, a map and
		// one mod that was unpacked one folder too deep.
		filepath.Join(userData, "Mods", "NeuesHUD", "ModInfo.xml"):                         modInfo("NeuesHUD", "Neues HUD", "1.2"),
		filepath.Join(userData, "Mods", "NeuesHUD", "XUi", "windows.xml"):                  "<configs/>",
		filepath.Join(userData, "Mods", "BessereKarte", "ModInfo.xml"):                     modInfo("BessereKarte", "Bessere Karte", "1.4"),
		filepath.Join(userData, "Mods", "MehrZombies", "ModInfo.xml"):                      modInfo("MehrZombies", "Mehr Zombies bei Nacht", "0.9"),
		filepath.Join(userData, "Mods", "SchnelleWerkbank", "ModInfo.xml"):                 modInfo("SchnelleWerkbank", "Schnellere Werkbank", "2.1"),
		filepath.Join(userData, "Mods", "Taschenlampe", "Taschenlampe", "ModInfo.xml"):     modInfo("Taschenlampe", "Bessere Taschenlampe", "1.0"),
		filepath.Join(userData, "Mods", "Taschenlampe", "Taschenlampe", "Config", "x.xml"): "<configs/>",

		// Minecraft with Fabric for 1.21.1 and two mods.
		filepath.Join(mc, "versions", "1.21.1", "1.21.1.json"):                                           `{"id":"1.21.1"}`,
		filepath.Join(mc, "versions", "fabric-loader-0.16.5-1.21.1", "fabric-loader-0.16.5-1.21.1.json"): `{"id":"fabric-loader-0.16.5-1.21.1","inheritsFrom":"1.21.1"}`,
		filepath.Join(mc, "launcher_profiles.json"):                                                      `{"profiles":{"fabric-loader-1.21.1":{"name":"fabric-loader-1.21.1","type":"custom","lastVersionId":"fabric-loader-0.16.5-1.21.1"}},"version":3}`,
	}
	for p, content := range files {
		if err := write(p, content); err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dl, 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(mc, "mods"), 0o755); err != nil {
		return nil, err
	}

	jars := map[string]map[string]string{
		filepath.Join(mc, "mods", "fabric-api-0.116.0+1.21.1.jar"):     {"fabric.mod.json": fabricMod("fabric-api", "Fabric API", "0.116.0+1.21.1", "~1.21.1")},
		filepath.Join(mc, "mods", "sodium-fabric-0.5.11+mc1.21.1.jar"): {"fabric.mod.json": fabricMod("sodium", "Sodium", "0.5.11+mc1.21.1", "~1.21.1")},
	}
	now := time.Now()
	downloads := []struct {
		name  string
		files map[string]string
		age   time.Duration
	}{
		{"NeuesHUD_v1.3.zip", map[string]string{
			"NeuesHUD/ModInfo.xml": modInfo("NeuesHUD", "Neues HUD", "1.3"), "NeuesHUD/XUi/windows.xml": "<configs/>",
		}, 5 * time.Minute},
		{"Fahrzeug-Pack-v2.zip", map[string]string{
			"Fahrzeug-Pack-v2/Mods/FahrzeugPack/ModInfo.xml":      modInfo("FahrzeugPack", "Fahrzeug-Pack", "2.0"),
			"Fahrzeug-Pack-v2/Mods/FahrzeugPack/FahrzeugPack.dll": "MZ",
			"Fahrzeug-Pack-v2/Liesmich.txt":                       "Viel Spass!",
		}, 9 * time.Minute},
		{"Rucksack60.zip", map[string]string{
			"ModInfo.xml": modInfo("BiggerBackpack60", "Größerer Rucksack (60 Plätze)", "1.0"), "Config/items.xml": "<configs/>",
		}, 20 * time.Hour},
		{"Mod-Sammlung.zip", map[string]string{
			"Sammlung/TolleKarte/ModInfo.xml": modInfo("TolleKarte", "Tolle Karte", "1.1"),
			"Sammlung/MehrLoot/ModInfo.xml":   modInfo("MehrLoot", "Mehr Loot", "3.0"),
		}, 26 * time.Hour},
		{"SchnelleWerkbank_2.1.zip", map[string]string{
			"SchnelleWerkbank/ModInfo.xml": modInfo("SchnelleWerkbank", "Schnellere Werkbank", "2.1"),
		}, 30 * 24 * time.Hour},
		{"Seamless Co-op v1.8.6.zip", map[string]string{
			"SeamlessCoop/ersc.dll":           "MZ",
			"SeamlessCoop/ersc_settings.ini":  "[GAMEPLAY]\nallow_invaders = 1\n\n[PASSWORD]\ncooppassword = \n\n[SAVE]\nsave_file_extension = co2\n",
			"SeamlessCoop/locale/german.json": "{}",
			"ersc_launcher.exe":               "MZ",
		}, 15 * time.Minute},
		{"Elden Mod Loader 1.4.zip", map[string]string{
			"dinput8.dll": "MZ", "mod_loader_config.ini": "[modloader]\nload_delay = 5000\n", "mods/.keep": "",
		}, 50 * time.Hour},
		{"SchnellerRollen.zip", map[string]string{
			"mods/SchnellerRollen.dll": "MZ", "mods/SchnellerRollen.ini": "[roll]\nspeed = 2\n",
		}, 49 * time.Hour},
		{"Elden Ring Reforged.zip", map[string]string{
			"regulation.bin": "x", "parts/am_m_1000.partsbnd.dcx": "x", "launchmod_eldenring.bat": "x",
		}, 72 * time.Hour},
		{"Faithful 32x.zip", map[string]string{
			"pack.mcmeta": `{"pack":{"pack_format":34,"description":"Faithful"}}`, "assets/minecraft/textures/block/stone.png": "png",
		}, 3 * time.Hour},
		{"Urlaubsfotos_2025.zip", map[string]string{"IMG_0001.jpg": "jpg", "IMG_0002.jpg": "jpg"}, 11 * 24 * time.Hour},
		{"Grafiktreiber_Setup.zip", map[string]string{"setup.exe": "MZ"}, 40 * 24 * time.Hour},
	}
	for _, d := range downloads {
		p := filepath.Join(dl, d.name)
		if err := writeZip(p, d.files); err != nil {
			return nil, err
		}
		_ = os.Chtimes(p, now.Add(-d.age), now.Add(-d.age))
	}
	jars[filepath.Join(dl, "lithium-fabric-0.14.0+mc1.21.1.jar")] = map[string]string{"fabric.mod.json": fabricMod("lithium", "Lithium", "0.14.0+mc1.21.1", "1.21.1")}
	jars[filepath.Join(dl, "sodium-fabric-0.6.13+mc1.21.1.jar")] = map[string]string{"fabric.mod.json": fabricMod("sodium", "Sodium", "0.6.13+mc1.21.1", "~1.21.1")}
	jars[filepath.Join(dl, "iris-fabric-1.9.0+mc1.21.5.jar")] = map[string]string{"fabric.mod.json": fabricMod("iris", "Iris", "1.9.0+mc1.21.5", ">=1.21.5")}
	jars[filepath.Join(dl, "create-1.21.1-6.0.6.jar")] = map[string]string{"META-INF/neoforge.mods.toml": neoforgeMod("create", "Create", "6.0.6", "[1.21.1,1.21.2)")}
	for p, f := range jars {
		if err := writeZip(p, f); err != nil {
			return nil, err
		}
		_ = os.Chtimes(p, now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	}

	return map[string]string{
		"ANYINSTALL_STEAM":         steam,
		"ANYINSTALL_7DTD_USERDATA": userData,
		"ANYINSTALL_MINECRAFT":     mc,
		"ANYINSTALL_DOWNLOADS":     dl,
		"ANYINSTALL_DATA":          data,
		"ANYINSTALL_DEMO":          root,
	}, nil
}

func manifest(id int, name, dir string) string {
	return fmt.Sprintf("\"AppState\"\n{\n\t\"appid\"\t\t\"%d\"\n\t\"name\"\t\t\"%s\"\n\t\"installdir\"\t\t\"%s\"\n\t\"StateFlags\"\t\t\"4\"\n}\n", id, name, dir)
}

func modInfo(name, display, version string) string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<xml>\n\t<Name value=\"%s\" />\n\t<DisplayName value=\"%s\" />\n\t<Version value=\"%s\" />\n\t<Author value=\"Demo\" />\n</xml>\n", name, display, version)
}

func fabricMod(id, name, version, mc string) string {
	return fmt.Sprintf(`{"schemaVersion":1,"id":%q,"name":%q,"version":%q,"depends":{"fabricloader":">=0.15","minecraft":%q,"fabric-api":"*"}}`, id, name, version, mc)
}

func neoforgeMod(id, name, version, mc string) string {
	return fmt.Sprintf("modLoader=\"javafml\"\nloaderVersion=\"[4,)\"\nlicense=\"MIT\"\n[[mods]]\nmodId=%q\nversion=%q\ndisplayName=%q\n[[dependencies.%s]]\nmodId=\"neoforge\"\nversionRange=\"[21.1.0,)\"\n[[dependencies.%s]]\nmodId=\"minecraft\"\nversionRange=%q\n", id, version, name, id, id, mc)
}

func write(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// writeZip writes a zip file. Names ending in "/.keep" only create the
// folder.
func writeZip(p string, files map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		if strings.HasSuffix(name, "/.keep") {
			if _, err := zw.Create(strings.TrimSuffix(name, ".keep")); err != nil {
				return err
			}
			continue
		}
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// WriteZip is exported for tests.
func WriteZip(p string, files map[string]string) error { return writeZip(p, files) }
