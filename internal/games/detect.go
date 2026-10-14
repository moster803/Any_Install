package games

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Mod systems that Any Install recognises in any game, without knowing
// the game itself.
const (
	SysBepInEx = "bepinex"     // BepInEx: plugins in BepInEx/plugins
	SysMelon   = "melonloader" // MelonLoader: DLLs in Mods
	SysUnreal  = "unreal"      // Unreal Engine: .pak files in Content/Paks/~mods
	SysModsDir = "modsdir"     // the game has its own Mods folder
)

// layout is what a look into a game folder found out.
type layout struct {
	dir       string
	system    string
	modsDir   string // where mods go
	disabled  string // where switched off mods are kept
	loader    bool   // BepInEx or MelonLoader is installed
	unity     string // "mono", "il2cpp" or ""
	project   string // Unreal: the project folder, e.g. <game>/Pal
	windows   bool   // the Windows version is installed (Proton on Linux)
	antiCheat bool
	signed    bool     // Unreal: the game's paks are signed, so mods are ignored
	exes      []string // program names, to see whether the game runs
}

// antiCheatDirs and antiCheatFile recognise anti-cheat systems by their
// files. A hit means a ban is possible when playing online with mods.
var antiCheatDirs = map[string]bool{
	"easyanticheat": true, "easyanticheat_eos": true, "battleye": true, "gameguard": true,
	"xigncode": true, "anticheatexpert": true,
}

func antiCheatFile(lower string) bool {
	for _, p := range []string{"start_protected_game.exe", "easyanticheat", "beservice", "beclient", "belauncher", "eaanticheat", "eac_server"} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return strings.HasSuffix(lower, "_be.exe")
}

// inspect looks at a game folder. It only reads folder listings, so it is
// cheap enough to run for every installed game.
func inspect(dir string) layout {
	l := layout{dir: dir}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return l
	}
	var mods string
	for _, e := range ents {
		name := e.Name()
		lower := strings.ToLower(name)
		full := filepath.Join(dir, name)
		if e.IsDir() {
			switch {
			case lower == "bepinex":
				if hasAny(filepath.Join(full, "core"), "BepInEx.dll", "BepInEx.Preloader.dll", "BepInEx.Core.dll", "BepInEx.Unity.Mono.dll", "BepInEx.Unity.IL2CPP.dll") {
					l.system, l.loader = SysBepInEx, true
				}
			case lower == "melonloader":
				if l.system == "" {
					l.system, l.loader = SysMelon, true
				}
			case lower == "mods":
				mods = full
			case antiCheatDirs[lower]:
				l.antiCheat = true
			case strings.HasSuffix(lower, "_data"):
				if exists(filepath.Join(full, "Managed", "Assembly-CSharp.dll")) {
					l.unity = "mono"
				}
			case lower != "engine" && l.project == "" && hasPak(filepath.Join(full, "Content", "Paks")):
				l.project = full
			}
			continue
		}
		switch {
		case lower == "gameassembly.dll" || lower == "gameassembly.so":
			l.unity = "il2cpp"
		case antiCheatFile(lower):
			l.antiCheat = true
		case strings.HasSuffix(lower, ".exe"):
			l.windows = true
			if !strings.Contains(lower, "unins") && !strings.Contains(lower, "crash") && !strings.Contains(lower, "setup") {
				l.exes = append(l.exes, name)
			}
		case strings.HasSuffix(lower, ".x86_64") || strings.HasSuffix(lower, ".x86"):
			l.exes = append(l.exes, name)
		}
	}
	if l.project == "" && hasPak(filepath.Join(dir, "Content", "Paks")) {
		l.project = dir // an Unreal game without a project folder
	}
	if l.project != "" {
		// Unreal games start <Project>/Binaries/Win64/<Project>-Win64-Shipping.exe.
		if bins, err := os.ReadDir(filepath.Join(l.project, "Binaries", "Win64")); err == nil {
			for _, b := range bins {
				lower := strings.ToLower(b.Name())
				if strings.HasSuffix(lower, ".exe") {
					l.windows = true
					l.exes = append(l.exes, b.Name())
				}
				if antiCheatDirs[lower] || antiCheatFile(lower) {
					l.antiCheat = true
				}
			}
		}
		l.signed = hasSigned(filepath.Join(l.project, "Content", "Paks"))
	}
	if !l.windows {
		// Many games keep their program in a sub-folder
		// (bin/win_x64/eurotrucks2.exe, Game/Bin/TS4_x64.exe).
		nested := nestedExes(dir)
		l.exes = append(l.exes, nested...)
		l.windows = len(nested) > 0
	}
	switch {
	case l.system == SysBepInEx:
		l.modsDir = filepath.Join(dir, "BepInEx", "plugins")
		l.disabled = filepath.Join(dir, "BepInEx", "plugins_disabled")
	case l.system == SysMelon:
		l.modsDir = filepath.Join(dir, "Mods")
		if mods != "" {
			l.modsDir = mods
		}
		l.disabled = l.modsDir + "_disabled"
	case l.project != "":
		l.system = SysUnreal
		l.modsDir = filepath.Join(l.project, "Content", "Paks", "~mods")
		// Outside of Paks: the engine loads every pak in there, also from
		// sub-folders.
		l.disabled = filepath.Join(l.project, "Content", "Paks_disabled")
	case mods != "":
		l.system = SysModsDir
		l.modsDir = mods
		l.disabled = mods + "_disabled"
	}
	return l
}

func hasAny(dir string, names ...string) bool {
	for _, n := range names {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}

// nestedExes finds Windows programs up to three folders deep. The search
// is bounded, as game folders can be huge.
func nestedExes(dir string) []string {
	var out []string
	seen := 0
	base := strings.Count(filepath.Clean(dir), string(filepath.Separator))
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > 5000 {
			return filepath.SkipAll
		}
		lower := strings.ToLower(d.Name())
		if d.IsDir() {
			if p != dir && (strings.Count(p, string(filepath.Separator))-base > 3 || strings.Contains(lower, "redist") || lower == "directx") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(lower, ".exe") && !strings.Contains(lower, "unins") && !strings.Contains(lower, "crash") && !strings.Contains(lower, "setup") {
			out = append(out, d.Name())
		}
		return nil
	})
	return out
}

// hasSigned reports whether the game's own paks come with .sig files. Such
// games check their paks and do not load mods.
func hasSigned(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".sig") {
			return true
		}
	}
	return false
}

func hasPak(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".pak") {
			return true
		}
	}
	return false
}

// proton reports whether a game runs through Proton: on Linux, only
// Steam's Windows version of a game comes with .exe files.
// The demo pretends to be a Windows computer.
func (l layout) proton() bool {
	return runtime.GOOS != "windows" && l.windows && os.Getenv("ANYINSTALL_DEMO") == ""
}
