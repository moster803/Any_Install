package install

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moster803/Any_Install/internal/archive"
)

func zipSource(t *testing.T, files map[string]string) archive.Source {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mod.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	f.Close()
	src, err := archive.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { src.Close() })
	return src
}

func read(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return "<missing>"
	}
	return string(b)
}

func write(p, s string) {
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(s), 0o644)
}

func TestReplaceUpdateAndUndo(t *testing.T) {
	game := t.TempDir()
	mods := filepath.Join(game, "Mods")
	write(filepath.Join(mods, "HUD", "ModInfo.xml"), "old")
	write(filepath.Join(mods, "HUD", "only-in-old.txt"), "x")
	m := &Manager{Root: filepath.Join(game, "AnyInstall-Backup"), Allowed: []string{game}, Keep: 10}

	src := zipSource(t, map[string]string{"Wrap/HUD/ModInfo.xml": "new", "Wrap/HUD/XUi/a.xml": "a"})
	tx, err := m.Begin("7dtd", "install")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply(src, Plan{Ops: []Op{{Kind: ReplaceDir, Src: "Wrap/HUD", Dst: filepath.Join(mods, "HUD")}}}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit([]string{"HUD"}); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(mods, "HUD", "ModInfo.xml")) != "new" || read(filepath.Join(mods, "HUD", "XUi", "a.xml")) != "a" {
		t.Fatal("new version not installed")
	}
	if _, err := os.Stat(filepath.Join(mods, "HUD", "only-in-old.txt")); err == nil {
		t.Fatal("old files must not survive a replace")
	}
	if h := m.History(); len(h) != 1 || h[0].Mods[0] != "HUD" {
		t.Fatalf("history = %+v", h)
	}

	if _, err := m.UndoLast(); err != nil {
		t.Fatal(err)
	}
	if read(filepath.Join(mods, "HUD", "ModInfo.xml")) != "old" || read(filepath.Join(mods, "HUD", "only-in-old.txt")) != "x" {
		t.Fatal("undo did not restore the old version")
	}
	if _, err := os.Stat(filepath.Join(mods, "HUD", "XUi")); err == nil {
		t.Fatal("undo left new files behind")
	}
	if _, err := m.UndoLast(); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("second undo: %v", err)
	}
}

func TestMergePutRemoveAndUndo(t *testing.T) {
	game := t.TempDir()
	gdir := filepath.Join(game, "Game")
	write(filepath.Join(gdir, "eldenring.exe"), "game")
	write(filepath.Join(gdir, "mods", "other.dll"), "keep me")
	write(filepath.Join(gdir, "mods", "roll.dll"), "old roll")
	write(filepath.Join(gdir, "old_launcher.exe"), "old")
	m := &Manager{Root: filepath.Join(game, "AnyInstall-Backup"), Allowed: []string{game}, Keep: 10}

	src := zipSource(t, map[string]string{
		"mods/roll.dll": "new roll", "mods/roll.ini": "ini", "mods/sub/x.txt": "x",
		"launcher.exe": "launcher",
	})
	tx, _ := m.Begin("eldenring", "install")
	err := tx.Apply(src, Plan{Ops: []Op{
		{Kind: MergeDir, Src: "mods", Dst: filepath.Join(gdir, "mods")},
		{Kind: PutFile, Src: "launcher.exe", Dst: filepath.Join(gdir, "ersc_launcher.exe")},
		{Kind: PutSelf, Dst: filepath.Join(gdir, "copy.zip")},
		{Kind: Remove, Dst: filepath.Join(gdir, "old_launcher.exe")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tx.Commit([]string{"roll"})
	if read(filepath.Join(gdir, "mods", "roll.dll")) != "new roll" || read(filepath.Join(gdir, "mods", "other.dll")) != "keep me" ||
		read(filepath.Join(gdir, "mods", "sub", "x.txt")) != "x" || read(filepath.Join(gdir, "ersc_launcher.exe")) != "launcher" {
		t.Fatal("merge/put failed")
	}
	if _, err := os.Stat(filepath.Join(gdir, "copy.zip")); err != nil {
		t.Fatal("put-self failed")
	}
	if _, err := os.Stat(filepath.Join(gdir, "old_launcher.exe")); err == nil {
		t.Fatal("remove failed")
	}

	if _, err := m.UndoLast(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]string{
		"mods/roll.dll": "old roll", "mods/other.dll": "keep me", "old_launcher.exe": "old",
		"mods/roll.ini": "<missing>", "ersc_launcher.exe": "<missing>", "copy.zip": "<missing>",
	} {
		if got := read(filepath.Join(gdir, filepath.FromSlash(p))); got != want {
			t.Errorf("%s = %q, want %q", p, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(gdir, "mods", "sub")); err == nil {
		t.Error("folder created by the install was not removed on undo")
	}
}

func TestOutsideIsRefusedAndRolledBack(t *testing.T) {
	game := t.TempDir()
	elsewhere := t.TempDir()
	m := &Manager{Root: filepath.Join(game, "AnyInstall-Backup"), Allowed: []string{game}}
	src := zipSource(t, map[string]string{"a/b.txt": "b"})
	tx, _ := m.Begin("x", "install")
	err := tx.Apply(src, Plan{Ops: []Op{
		{Kind: ReplaceDir, Src: "a", Dst: filepath.Join(game, "Mods", "A")},
		{Kind: ReplaceDir, Src: "a", Dst: filepath.Join(elsewhere, "A")},
	}})
	if !errors.Is(err, ErrOutside) {
		t.Fatalf("err = %v, want ErrOutside", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(game, "Mods", "A")); err == nil {
		t.Fatal("rollback left the first mod in place")
	}
	if _, err := os.Stat(filepath.Join(game, "Mods")); err == nil {
		t.Fatal("rollback left the created Mods folder")
	}
	if len(m.History()) != 0 {
		t.Fatal("rolled back transaction in history")
	}
}

func TestKeepIniAndPrune(t *testing.T) {
	game := t.TempDir()
	ini := filepath.Join(game, "Game", "SeamlessCoop", "ersc_settings.ini")
	write(ini, "[PASSWORD]\ncooppassword = geheim\n[GAMEPLAY]\nallow_invaders = 0\n")
	m := &Manager{Root: filepath.Join(game, "AnyInstall-Backup"), Allowed: []string{game}, Keep: 2}
	for i := 0; i < 3; i++ {
		src := zipSource(t, map[string]string{"SeamlessCoop/ersc_settings.ini": "[GAMEPLAY]\nallow_invaders = 1\nnew_option = 5\n\n[PASSWORD]\ncooppassword = \n"})
		tx, _ := m.Begin("eldenring", "install")
		if err := tx.Apply(src, Plan{
			Ops:     []Op{{Kind: ReplaceDir, Src: "SeamlessCoop", Dst: filepath.Dir(ini)}},
			KeepIni: []string{ini},
		}); err != nil {
			t.Fatal(err)
		}
		tx.Commit([]string{"coop"})
	}
	got := read(ini)
	if !strings.Contains(got, "cooppassword = geheim") || !strings.Contains(got, "allow_invaders = 0") || !strings.Contains(got, "new_option = 5") {
		t.Fatalf("ini not merged:\n%s", got)
	}
	if n := len(m.History()); n != 2 {
		t.Fatalf("history has %d entries, want 2 after pruning", n)
	}
}

func TestSetIniValue(t *testing.T) {
	if got := SetIniValue("[PASSWORD]\r\ncooppassword = \r\n", "PASSWORD", "cooppassword", "abc"); got != "[PASSWORD]\r\ncooppassword = abc\r\n" {
		t.Errorf("replace: %q", got)
	}
	if got := SetIniValue("[A]\nx = 1\n", "PASSWORD", "cooppassword", "abc"); !strings.HasSuffix(got, "[PASSWORD]\ncooppassword = abc\n") {
		t.Errorf("append: %q", got)
	}
	if got := SetIniValue("[PASSWORD]\nfoo = 1\n", "PASSWORD", "cooppassword", "abc"); got != "[PASSWORD]\ncooppassword = abc\nfoo = 1\n" {
		t.Errorf("insert into section: %q", got)
	}
}
