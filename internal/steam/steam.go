package steam

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Steam is a found Steam installation.
type Steam struct {
	Root      string
	Libraries []string
}

// App is an installed Steam game.
type App struct {
	AppID   int
	Name    string
	Dir     string // full path to the game folder
	Library string
}

// Find locates Steam. It returns nil if Steam is not installed.
func Find() *Steam {
	for _, root := range candidateRoots() {
		if isSteamRoot(root) {
			s := &Steam{Root: root}
			s.Libraries = libraries(root)
			return s
		}
	}
	return nil
}

func candidateRoots() []string {
	var out []string
	if env := os.Getenv("ANYINSTALL_STEAM"); env != "" {
		out = append(out, filepath.SplitList(env)...)
		return out
	}
	out = append(out, osRoots()...)
	seen := map[string]bool{}
	var uniq []string
	for _, r := range out {
		if r == "" {
			continue
		}
		key := r
		if real, err := filepath.EvalSymlinks(r); err == nil {
			key = real
		}
		key = strings.ToLower(filepath.Clean(key))
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, r)
	}
	return uniq
}

func isSteamRoot(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "steamapps"))
	return err == nil && st.IsDir()
}

// libraries reads libraryfolders.vdf and returns every existing library,
// starting with the Steam folder itself.
func libraries(root string) []string {
	libs := []string{root}
	for _, f := range []string{
		filepath.Join(root, "steamapps", "libraryfolders.vdf"),
		filepath.Join(root, "config", "libraryfolders.vdf"),
	} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		kv, err := ParseVDF(string(b))
		if err != nil {
			continue
		}
		block := kv.Get("libraryfolders")
		for key, v := range block {
			if _, err := strconv.Atoi(key); err != nil {
				continue
			}
			var p string
			switch val := v.(type) {
			case string: // old format: "1" "D:\\SteamLibrary"
				p = val
			case KV: // new format: "1" { "path" "D:\\SteamLibrary" ... }
				p = val.Str("path")
			}
			if p != "" {
				libs = append(libs, p)
			}
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, l := range libs {
		clean := filepath.Clean(l)
		key := strings.ToLower(clean)
		if real, err := filepath.EvalSymlinks(clean); err == nil {
			key = strings.ToLower(real)
		}
		if seen[key] || !isSteamRoot(clean) {
			continue
		}
		seen[key] = true
		out = append(out, clean)
	}
	return out
}

// Apps lists every installed game in all libraries.
func (s *Steam) Apps() []App {
	var apps []App
	for _, lib := range s.Libraries {
		matches, _ := filepath.Glob(filepath.Join(lib, "steamapps", "appmanifest_*.acf"))
		for _, m := range matches {
			if app, ok := readManifest(lib, m); ok {
				apps = append(apps, app)
			}
		}
	}
	sort.Slice(apps, func(i, j int) bool { return strings.ToLower(apps[i].Name) < strings.ToLower(apps[j].Name) })
	return apps
}

// App finds one installed game by its Steam app id.
func (s *Steam) App(id int) (App, bool) {
	for _, lib := range s.Libraries {
		m := filepath.Join(lib, "steamapps", "appmanifest_"+strconv.Itoa(id)+".acf")
		if app, ok := readManifest(lib, m); ok {
			return app, true
		}
	}
	return App{}, false
}

func readManifest(lib, file string) (App, bool) {
	b, err := os.ReadFile(file)
	if err != nil {
		return App{}, false
	}
	kv, err := ParseVDF(string(b))
	if err != nil {
		return App{}, false
	}
	st := kv.Get("AppState")
	id, _ := strconv.Atoi(st.Str("appid"))
	dir := st.Str("installdir")
	if id == 0 || dir == "" {
		return App{}, false
	}
	full := filepath.Join(lib, "steamapps", "common", dir)
	if info, err := os.Stat(full); err != nil || !info.IsDir() {
		return App{}, false
	}
	name := st.Str("name")
	if name == "" {
		name = dir
	}
	return App{AppID: id, Name: name, Dir: full, Library: lib}, true
}

// Cover returns the path of the game's header image from Steam's local
// image cache, or "" if there is none.
func (s *Steam) Cover(id int) string {
	cache := filepath.Join(s.Root, "appcache", "librarycache")
	sid := strconv.Itoa(id)
	for _, name := range []string{sid + "_header.jpg", filepath.Join(sid, "header.jpg")} {
		p := filepath.Join(cache, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	// Newer Steam clients keep images in hashed sub-folders.
	var found string
	_ = filepath.WalkDir(filepath.Join(cache, sid), func(p string, d os.DirEntry, err error) error {
		if err != nil || found != "" {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.EqualFold(d.Name(), "header.jpg") {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// CompatTool returns the Steam Play compatibility tool the user forced for
// a game in its properties (for example "proton_9" or "GE-Proton9-20"), or
// "" when none is set. ok is false when Steam's settings could not be read.
func (s *Steam) CompatTool(appID int) (name string, ok bool) {
	b, err := os.ReadFile(filepath.Join(s.Root, "config", "config.vdf"))
	if err != nil {
		return "", false
	}
	kv, err := ParseVDF(string(b))
	if err != nil {
		return "", false
	}
	m := kv.Get("InstallConfigStore").Get("Software").Get("Valve").Get("Steam").Get("CompatToolMapping")
	return m.Get(strconv.Itoa(appID)).Str("name"), true
}
