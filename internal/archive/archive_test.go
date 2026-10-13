package archive

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeZip(t *testing.T, p string, files map[string]string) {
	t.Helper()
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	zw.Close()
	f.Close()
}

func names(entries []Entry) []string {
	var out []string
	for _, e := range entries {
		n := e.Name
		if e.Dir {
			n += "/"
		}
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func TestCleanName(t *testing.T) {
	for in, want := range map[string]string{
		"a/b.txt":      "a/b.txt",
		`a\b.txt`:      "a/b.txt",
		"./a/b/":       "a/b",
		"a//b":         "a/b",
		"../evil.txt":  "!",
		"a/../../evil": "!",
		"/etc/passwd":  "!",
		`C:\Windows`:   "!",
		"a/..b/c":      "a/..b/c",
	} {
		got, ok := CleanName(in)
		if !ok {
			got = "!"
		}
		if got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestZipEntriesAndExtract(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "mod.zip")
	writeZip(t, p, map[string]string{
		"Wrapper/Mods/MyMod/ModInfo.xml": "<xml/>",
		"Wrapper/Mods/MyMod/Config/a":    "a",
		"Wrapper/readme.txt":             "hi",
		"__MACOSX/._junk":                "x",
	})
	src, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ents, err := src.Entries()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(ents), ",")
	want := "Wrapper/,Wrapper/Mods/,Wrapper/Mods/MyMod/,Wrapper/Mods/MyMod/Config/,Wrapper/Mods/MyMod/Config/a,Wrapper/Mods/MyMod/ModInfo.xml,Wrapper/readme.txt"
	if got != want {
		t.Fatalf("entries:\n got %s\nwant %s", got, want)
	}
	data, err := src.ReadFiles([]string{"Wrapper/Mods/MyMod/ModInfo.xml", "missing"}, 1024)
	if err != nil || string(data["Wrapper/Mods/MyMod/ModInfo.xml"]) != "<xml/>" || len(data) != 1 {
		t.Fatalf("ReadFiles = %v, %v", data, err)
	}
	out := filepath.Join(dir, "out")
	if err := src.Extract("Wrapper/Mods/MyMod", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "Config", "a")); string(b) != "a" {
		t.Fatal("subtree not extracted")
	}
	if _, err := os.Stat(filepath.Join(out, "readme.txt")); err == nil {
		t.Fatal("file outside of prefix extracted")
	}
	single := filepath.Join(dir, "single.txt")
	if err := src.Extract("Wrapper/readme.txt", single); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(single); string(b) != "hi" {
		t.Fatal("single file not extracted")
	}
}

func TestZipSlipIsRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "evil.zip")
	writeZip(t, p, map[string]string{"ok.txt": "ok", "../../evil.txt": "evil"})
	src, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Entries(); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Entries err = %v, want ErrUnsafe", err)
	}
	if err := src.Extract("", filepath.Join(dir, "out")); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Extract err = %v, want ErrUnsafe", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "evil.txt")); err == nil {
		t.Fatal("evil file was written")
	}
}

func TestSevenZip(t *testing.T) {
	src, err := Open(filepath.Join("testdata", "backpack.7z"))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ents, err := src.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(ents), ","); got != "Config/,Config/items.xml,ModInfo.xml" {
		t.Fatalf("entries = %s", got)
	}
	data, _ := src.ReadFiles([]string{"ModInfo.xml"}, 4096)
	if !strings.Contains(string(data["ModInfo.xml"]), "BiggerBackpack60") {
		t.Fatalf("ModInfo.xml = %q", data["ModInfo.xml"])
	}
	out := t.TempDir()
	if err := src.Extract("", out); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "Config", "items.xml")); err != nil {
		t.Fatal(err)
	}
}

func TestRar(t *testing.T) {
	for _, f := range []string{"rar3-solid.rar", "rar5-solid.rar"} {
		t.Run(f, func(t *testing.T) {
			src, err := Open(filepath.Join("testdata", f))
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			ents, err := src.Entries()
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Join(names(ents), ","); got != "stest1.txt,stest2.txt" {
				t.Fatalf("entries = %s", got)
			}
			// The second file of a solid archive needs the first one decoded.
			data, err := src.ReadFiles([]string{"stest2.txt"}, 1<<20)
			if err != nil || len(data["stest2.txt"]) != 2048 {
				t.Fatalf("ReadFiles: %d bytes, %v", len(data["stest2.txt"]), err)
			}
			out := t.TempDir()
			if err := src.Extract("", out); err != nil {
				t.Fatal(err)
			}
			if st, err := os.Stat(filepath.Join(out, "stest1.txt")); err != nil || st.Size() != 2048 {
				t.Fatalf("extract: %v", err)
			}
		})
	}
}

func TestDirSource(t *testing.T) {
	root := t.TempDir()
	mod := filepath.Join(root, "MyMod")
	os.MkdirAll(filepath.Join(mod, "Inner", "Config"), 0o755)
	os.WriteFile(filepath.Join(mod, "Inner", "ModInfo.xml"), []byte("<xml/>"), 0o644)
	os.WriteFile(filepath.Join(mod, "Inner", "Config", "a.xml"), []byte("a"), 0o644)
	src, err := Open(mod)
	if err != nil {
		t.Fatal(err)
	}
	ents, _ := src.Entries()
	if got := strings.Join(names(ents), ","); got != "MyMod/,MyMod/Inner/,MyMod/Inner/Config/,MyMod/Inner/Config/a.xml,MyMod/Inner/ModInfo.xml" {
		t.Fatalf("entries = %s", got)
	}
	data, _ := src.ReadFiles([]string{"MyMod/Inner/ModInfo.xml", "MyMod/../../etc/passwd"}, 1024)
	if string(data["MyMod/Inner/ModInfo.xml"]) != "<xml/>" || len(data) != 1 {
		t.Fatalf("ReadFiles = %v", data)
	}
	out := filepath.Join(t.TempDir(), "x")
	if err := src.Extract("MyMod/Inner", out); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "Config", "a.xml")); string(b) != "a" {
		t.Fatal("dir subtree not copied")
	}
}
