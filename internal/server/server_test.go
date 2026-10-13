package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moster803/Any_Install/internal/demo"
)

// testServer starts the server on the demo world.
func testServer(t *testing.T) (*Server, string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	env, err := demo.Build(root)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
	web := fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>Any Install</title>")}}
	s := New("test", env["ANYINSTALL_DATA"], env["ANYINSTALL_DOWNLOADS"], web)
	url, err := s.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	base := strings.SplitN(url, "/?t=", 2)[0]
	return s, base, env
}

func call(t *testing.T, s *Server, method, url string, body any, out any) int {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, url, r)
	req.Header.Set("X-Token", s.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

type scanResp struct {
	Items []struct {
		Path string `json:"path"`
		File string `json:"file"`
		Mods []struct {
			Key     string `json:"key"`
			Name    string `json:"name"`
			Status  string `json:"status"`
			Checked bool   `json:"checked"`
		} `json:"mods"`
	} `json:"items"`
	Others []struct {
		File string `json:"file"`
	} `json:"others"`
}

func TestAuth(t *testing.T) {
	_, base, _ := testServer(t)
	resp, err := http.Get(base + "/api/state")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("without token: %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", base+"/", nil)
	req.Host = "evil.example"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign host: %d", resp.StatusCode)
	}
}

func TestScanInstallUndo7DaysToDie(t *testing.T) {
	s, base, env := testServer(t)
	var scan scanResp
	if code := call(t, s, "GET", base+"/api/scan?game=7dtd", nil, &scan); code != 200 {
		t.Fatalf("scan: %d", code)
	}
	type item struct {
		Path string `json:"path"`
		Key  string `json:"key"`
	}
	var items []item
	status := map[string]string{}
	for _, it := range scan.Items {
		for _, m := range it.Mods {
			status[m.Name] = m.Status
			if m.Checked {
				items = append(items, item{it.Path, m.Key})
			}
		}
	}
	want := map[string]string{
		"Neues HUD": "update", "Fahrzeug-Pack": "new", "Größerer Rucksack (60 Plätze)": "new",
		"Tolle Karte": "new", "Mehr Loot": "new", "Schnellere Werkbank": "installed",
	}
	for name, st := range want {
		if status[name] != st {
			t.Errorf("%s: status %q, want %q", name, status[name], st)
		}
	}
	if len(items) != 5 {
		t.Fatalf("%d mods pre-selected, want 5", len(items))
	}
	if len(scan.Others) == 0 {
		t.Error("photos and other games' mods should be listed as others")
	}

	var res struct {
		Installed []struct{ Name, Status string } `json:"installed"`
		Tips      []struct{ Code string }         `json:"tips"`
	}
	if code := call(t, s, "POST", base+"/api/install", map[string]any{"game": "7dtd", "items": items}, &res); code != 200 {
		t.Fatalf("install: %d", code)
	}
	mods := filepath.Join(env["ANYINSTALL_7DTD_USERDATA"], "Mods")
	for _, p := range []string{"NeuesHUD/ModInfo.xml", "FahrzeugPack/FahrzeugPack.dll", "BiggerBackpack60/ModInfo.xml", "TolleKarte/ModInfo.xml", "MehrLoot/ModInfo.xml"} {
		if _, err := os.Stat(filepath.Join(mods, filepath.FromSlash(p))); err != nil {
			t.Errorf("missing after install: %s", p)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(mods, "NeuesHUD", "ModInfo.xml")); !strings.Contains(string(b), `"1.3"`) {
		t.Error("HUD not updated")
	}
	if len(res.Tips) == 0 || res.Tips[0].Code != "tip_7dtd_eac" {
		t.Errorf("tips = %+v", res.Tips)
	}

	if code := call(t, s, "POST", base+"/api/undo", map[string]any{"game": "7dtd"}, nil); code != 200 {
		t.Fatalf("undo: %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(mods, "NeuesHUD", "ModInfo.xml")); !strings.Contains(string(b), `"1.2"`) {
		t.Error("undo did not restore HUD 1.2")
	}
	if _, err := os.Stat(filepath.Join(mods, "FahrzeugPack")); err == nil {
		t.Error("undo left a new mod behind")
	}
}

func TestInstallRefusesForeignPaths(t *testing.T) {
	s, base, _ := testServer(t)
	secret := filepath.Join(t.TempDir(), "x.zip")
	os.WriteFile(secret, []byte("PK"), 0o644)
	code := call(t, s, "POST", base+"/api/install", map[string]any{"game": "7dtd", "items": []map[string]string{{"path": secret, "key": ""}}}, nil)
	if code != http.StatusBadRequest {
		t.Errorf("install from a foreign folder: %d", code)
	}
}

func TestEldenRingCoopPassword(t *testing.T) {
	s, base, _ := testServer(t)
	var scan scanResp
	call(t, s, "GET", base+"/api/scan?game=eldenring", nil, &scan)
	var coop map[string]string
	for _, it := range scan.Items {
		if strings.HasPrefix(it.File, "Seamless") {
			coop = map[string]string{"path": it.Path, "key": it.Mods[0].Key}
		}
	}
	if coop == nil {
		t.Fatal("Seamless Co-op not found in downloads")
	}
	if code := call(t, s, "POST", base+"/api/install", map[string]any{"game": "eldenring", "items": []map[string]string{coop}}, nil); code != 200 {
		t.Fatalf("install: %d", code)
	}
	if code := call(t, s, "POST", base+"/api/coop-password", map[string]string{"password": "Kumpelabend"}, nil); code != 200 {
		t.Fatalf("password: %d", code)
	}
	if code := call(t, s, "POST", base+"/api/coop-password", map[string]string{"password": "a;b"}, nil); code != http.StatusBadRequest {
		t.Errorf("bad password accepted: %d", code)
	}
	var inst struct {
		Coop struct {
			Installed bool
			Password  string
		} `json:"coop"`
		Items []struct{ Name, Version string } `json:"items"`
	}
	call(t, s, "GET", base+"/api/installed?game=eldenring", nil, &inst)
	if !inst.Coop.Installed || inst.Coop.Password != "Kumpelabend" {
		t.Errorf("coop = %+v", inst.Coop)
	}
	if len(inst.Items) == 0 || inst.Items[0].Version != "1.8.6" {
		t.Errorf("items = %+v", inst.Items)
	}
	// An update keeps the password.
	call(t, s, "POST", base+"/api/install", map[string]any{"game": "eldenring", "items": []map[string]string{coop}}, nil)
	call(t, s, "GET", base+"/api/installed?game=eldenring", nil, &inst)
	if inst.Coop.Password != "Kumpelabend" {
		t.Errorf("password lost on update: %q", inst.Coop.Password)
	}
}

func TestImportDroppedFile(t *testing.T) {
	s, base, _ := testServer(t)
	req, _ := http.NewRequest("POST", base+"/api/import?name=../../evil.exe", strings.NewReader("x"))
	req.Header.Set("X-Token", s.Token)
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("exe import: %d", resp.StatusCode)
	}
	jar := filepath.Join(t.TempDir(), "x.jar")
	demo.WriteZip(jar, map[string]string{"fabric.mod.json": `{"id":"dropped","name":"Dropped","version":"1","depends":{"minecraft":"1.21.1"}}`})
	b, _ := os.ReadFile(jar)
	req, _ = http.NewRequest("POST", base+"/api/import?name=dropped-1.jar", bytes.NewReader(b))
	req.Header.Set("X-Token", s.Token)
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("import: %d", resp.StatusCode)
	}
	var scan scanResp
	call(t, s, "GET", base+"/api/scan?game=minecraft", nil, &scan)
	if len(scan.Items) == 0 || scan.Items[0].File != "dropped-1.jar" {
		t.Errorf("dropped file should be listed first, got %+v", scan.Items)
	}
}

func TestGamePathMustContainTheGame(t *testing.T) {
	s, base, env := testServer(t)
	var e apiError
	if code := call(t, s, "POST", base+"/api/game-path", map[string]string{"game": "eldenring", "path": t.TempDir()}, &e); code != http.StatusBadRequest || e.Code != "wrong_folder" {
		t.Errorf("wrong folder accepted: %d %+v", code, e)
	}
	game := filepath.Join(env["ANYINSTALL_STEAM"], "steamapps", "common", "ELDEN RING", "Game")
	if code := call(t, s, "POST", base+"/api/game-path", map[string]string{"game": "eldenring", "path": game}, nil); code != 200 {
		t.Errorf("Game folder refused: %d", code)
	}
}
