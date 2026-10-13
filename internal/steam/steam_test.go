package steam

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseVDFNewFormat(t *testing.T) {
	kv, err := ParseVDF(`"libraryfolders"
{
	"0"
	{
		"path"		"C:\\Program Files (x86)\\Steam"
		"apps"
		{
			"228980"		"0"
		}
	}
	// a comment
	"1" { "path" "D:\\SteamLibrary" "label" "say \"hi\"" }
}`)
	if err != nil {
		t.Fatal(err)
	}
	lf := kv.Get("LibraryFolders")
	if got := lf.Get("0").Str("path"); got != `C:\Program Files (x86)\Steam` {
		t.Errorf("path 0 = %q", got)
	}
	if got := lf.Get("1").Str("label"); got != `say "hi"` {
		t.Errorf("label = %q", got)
	}
}

func TestParseVDFOldFormatAndConditionals(t *testing.T) {
	kv, err := ParseVDF("\ufeff\"LibraryFolders\"\n{\n\t\"TimeNextStatsReport\"\t\"1\"\n\t\"1\"\t\"/mnt/games\" [$LINUX]\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := kv.Get("libraryfolders").Str("1"); got != "/mnt/games" {
		t.Errorf("got %q", got)
	}
	if _, err := ParseVDF(`"a" { "b" "c"`); err == nil {
		t.Error("unterminated block should fail")
	}
}

func TestFindAppsInLibraries(t *testing.T) {
	root := t.TempDir()
	steam := filepath.Join(root, "Steam")
	lib := filepath.Join(root, "Lib2")
	write := func(p, s string) {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(s), 0o644)
	}
	write(filepath.Join(steam, "steamapps", "libraryfolders.vdf"),
		`"libraryfolders" { "0" { "path" "`+filepath.ToSlash(steam)+`" } "1" { "path" "`+filepath.ToSlash(lib)+`" } "2" { "path" "/does/not/exist" } }`)
	write(filepath.Join(lib, "steamapps", "appmanifest_251570.acf"), `"AppState" { "appid" "251570" "name" "7 Days to Die" "installdir" "7 Days To Die" }`)
	os.MkdirAll(filepath.Join(lib, "steamapps", "common", "7 Days To Die"), 0o755)
	// A manifest whose folder is missing (uninstalled) must be ignored.
	write(filepath.Join(steam, "steamapps", "appmanifest_1245620.acf"), `"AppState" { "appid" "1245620" "name" "ELDEN RING" "installdir" "ELDEN RING" }`)
	write(filepath.Join(steam, "appcache", "librarycache", "251570", "abc123", "header.jpg"), "jpg")

	t.Setenv("ANYINSTALL_STEAM", steam)
	s := Find()
	if s == nil {
		t.Fatal("steam not found")
	}
	if len(s.Libraries) != 2 {
		t.Fatalf("libraries = %v", s.Libraries)
	}
	app, ok := s.App(251570)
	if !ok || app.Name != "7 Days to Die" || app.Dir != filepath.Join(lib, "steamapps", "common", "7 Days To Die") {
		t.Fatalf("App = %+v %v", app, ok)
	}
	if _, ok := s.App(1245620); ok {
		t.Error("uninstalled game reported as installed")
	}
	if len(s.Apps()) != 1 {
		t.Errorf("Apps = %v", s.Apps())
	}
	if c := s.Cover(251570); filepath.Base(c) != "header.jpg" {
		t.Errorf("Cover = %q", c)
	}
}
