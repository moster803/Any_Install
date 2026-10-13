package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/games"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/steam"
	"github.com/moster803/Any_Install/internal/sys"
)

// ---------- state and settings ----------

type gameView struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	AppID    int            `json:"appId"`
	Location games.Location `json:"location"`
	HasCover bool           `json:"hasCover"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	env := s.env()
	var list []gameView
	for _, g := range games.All() {
		v := gameView{ID: g.ID(), Name: g.Name(), AppID: g.AppID(), Location: g.Locate(env)}
		if env.Steam != nil && g.AppID() > 0 {
			v.HasCover = env.Steam.Cover(g.AppID()) != ""
		}
		list = append(list, v)
	}
	steamInfo := map[string]any{"found": env.Steam != nil}
	if env.Steam != nil {
		steamInfo["root"] = env.Steam.Root
		steamInfo["libraries"] = len(env.Steam.Libraries)
		steamInfo["games"] = len(env.Steam.Apps())
	}
	s.mu.Lock()
	st := s.settings
	s.mu.Unlock()
	writeJSON(w, map[string]any{
		"version":      s.Version,
		"lang":         st.Lang,
		"game":         st.Game,
		"steam":        steamInfo,
		"games":        list,
		"downloads":    s.Download,
		"extraFolders": st.ExtraFolders,
		"os":           runtime.GOOS,
		"demo":         os.Getenv("ANYINSTALL_DEMO"),
	})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Lang *string `json:"lang"`
		Game *string `json:"game"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.mu.Lock()
	if req.Lang != nil && (*req.Lang == "de" || *req.Lang == "en") {
		s.settings.Lang = *req.Lang
	}
	if req.Game != nil && games.ByID(*req.Game) != nil {
		s.settings.Game = *req.Game
	}
	err := s.saveSettings()
	s.mu.Unlock()
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleGamePath(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game  string `json:"game"`
		Path  string `json:"path"`
		Pick  bool   `json:"pick"`
		Reset bool   `json:"reset"`
		Title string `json:"title"`
	}
	if err := decode(r, &req); err != nil || games.ByID(req.Game) == nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	p := strings.TrimSpace(req.Path)
	if req.Pick {
		var err error
		if p, err = sys.PickFolder(req.Title); err != nil {
			code := "cancelled"
			if errors.Is(err, sys.ErrNoPicker) {
				code = "no_picker"
			}
			fail(w, http.StatusConflict, code, nil)
			return
		}
	}
	if !req.Reset {
		if st, err := os.Stat(p); err != nil || !st.IsDir() {
			fail(w, http.StatusBadRequest, "folder_missing", err)
			return
		}
		// Make sure the game is really there before remembering the folder.
		env := s.env()
		env.Custom[req.Game] = p
		if g := games.ByID(req.Game); !g.Locate(env).Found {
			fail(w, http.StatusBadRequest, "wrong_folder", nil, "game", g.Name())
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if req.Reset {
		delete(s.settings.GamePaths, req.Game)
	} else {
		s.settings.GamePaths[req.Game] = p
	}
	if err := s.saveSettings(); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": p})
}

func (s *Server) handleFolders(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Add    string `json:"add"`
		Pick   bool   `json:"pick"`
		Remove string `json:"remove"`
		Title  string `json:"title"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	add := strings.TrimSpace(req.Add)
	if req.Pick {
		var err error
		if add, err = sys.PickFolder(req.Title); err != nil {
			code := "cancelled"
			if errors.Is(err, sys.ErrNoPicker) {
				code = "no_picker"
			}
			fail(w, http.StatusConflict, code, nil)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if add != "" {
		if st, err := os.Stat(add); err != nil || !st.IsDir() {
			fail(w, http.StatusBadRequest, "folder_missing", err)
			return
		}
		dup := false
		for _, f := range s.settings.ExtraFolders {
			dup = dup || samePath(f, add)
		}
		if !dup && !samePath(add, s.Download) {
			s.settings.ExtraFolders = append(s.settings.ExtraFolders, add)
		}
	}
	if req.Remove != "" {
		var keep []string
		for _, f := range s.settings.ExtraFolders {
			if !samePath(f, req.Remove) {
				keep = append(keep, f)
			}
		}
		s.settings.ExtraFolders = keep
	}
	if err := s.saveSettings(); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "extraFolders": s.settings.ExtraFolders})
}

func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// ---------- scanning ----------

type modView struct {
	Key              string       `json:"key"`
	Name             string       `json:"name"`
	Version          string       `json:"version,omitempty"`
	Kind             string       `json:"kind"`
	Status           string       `json:"status"`
	InstalledVersion string       `json:"installedVersion,omitempty"`
	Notes            []games.Note `json:"notes,omitempty"`
	Selectable       bool         `json:"selectable"`
	Checked          bool         `json:"checked"`
	Loader           string       `json:"loader,omitempty"`
	MCVersion        string       `json:"mcVersion,omitempty"`
	MCLabel          string       `json:"mcLabel,omitempty"`
	HasCode          bool         `json:"hasCode,omitempty"`
}

type itemView struct {
	Path     string    `json:"path"`
	File     string    `json:"file"`
	Kind     string    `json:"kind"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Imported bool      `json:"imported,omitempty"`
	Mods     []modView `json:"mods,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type source struct {
	path     string
	info     os.FileInfo
	imported bool
}

type analysis struct {
	found []games.Found
	err   string
}

// folders returns every folder that is searched for downloads.
func (s *Server) folders() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{s.Download}
	out = append(out, s.settings.ExtraFolders...)
	return append(out, s.importsDir())
}

func (s *Server) listSources() []source {
	var out []source
	for _, dir := range s.folders() {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		imported := samePath(dir, s.importsDir())
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "AnyInstall-Daten" {
				continue
			}
			if !e.IsDir() && !archive.SupportedExt(name) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, source{path: filepath.Join(dir, name), info: info, imported: imported})
		}
	}
	return out
}

// sourceAllowed makes sure an install request only touches downloads that
// the scan can list.
func (s *Server) sourceAllowed(p string) bool {
	parent := filepath.Dir(filepath.Clean(p))
	for _, f := range s.folders() {
		if samePath(parent, f) {
			return true
		}
	}
	return false
}

func (s *Server) analyze(g games.Game, p string, info os.FileInfo) analysis {
	key := fmt.Sprintf("%s|%s|%d|%d", g.ID(), p, info.Size(), info.ModTime().UnixNano())
	if v, ok := s.cache.Load(key); ok {
		return v.(analysis)
	}
	var a analysis
	src, err := archive.Open(p)
	if err == nil {
		var entries []archive.Entry
		entries, err = src.Entries()
		if err == nil {
			a.found = g.Analyze(src, entries)
		}
		src.Close()
	}
	if err != nil {
		a.err = errorCode(err)
		if a.err == "failed" {
			a.err = "broken"
		}
		if info.IsDir() {
			a.err = "" // big or unreadable folders are simply not mods
		}
	}
	s.cache.Store(key, a)
	return a
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	g, loc, ok := s.gameFor(r.URL.Query().Get("game"))
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	m := games.Manager(loc)
	inst := g.Installed(loc, m)
	sources := s.listSources()
	results := make([]analysis, len(sources))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, src := range sources {
		wg.Add(1)
		go func(i int, src source) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = s.analyze(g, src.path, src.info)
		}(i, src)
	}
	wg.Wait()

	items := []itemView{}
	others := []itemView{}
	for i, src := range sources {
		v := itemView{
			Path: src.path, File: filepath.Base(src.path), Size: src.info.Size(),
			Modified: src.info.ModTime(), Imported: src.imported, Kind: kindOf(src),
			Error: results[i].err,
		}
		if src.info.IsDir() {
			v.Size = 0
		}
		for _, f := range results[i].found {
			status, iv, notes := g.Status(loc, inst, f, m)
			sel := status == games.StatusNew || status == games.StatusUpdate || status == games.StatusOlder
			// Mods that would not work (wrong Minecraft version, missing
			// loader) are offered but not ticked.
			blocked := false
			for _, n := range notes {
				blocked = blocked || n.Level == "error"
			}
			v.Mods = append(v.Mods, modView{
				Key: f.Key, Name: f.Name, Version: f.Version, Kind: f.Kind, Status: status,
				InstalledVersion: iv, Notes: notes, Selectable: sel,
				Checked: (status == games.StatusNew || status == games.StatusUpdate) && !blocked,
				Loader:  f.Loader, MCVersion: f.MCVersion, MCLabel: games.DescribeMC(f.MCVersion), HasCode: f.HasCode,
			})
		}
		if len(v.Mods) > 0 {
			items = append(items, v)
		} else if !src.info.IsDir() || v.Error != "" {
			others = append(others, v)
		}
	}
	sortItems(items)
	sortItems(others)
	writeJSON(w, map[string]any{"items": items, "others": others, "folders": s.folders()[:len(s.folders())-1]})
}

func kindOf(src source) string {
	if src.info.IsDir() {
		return "dir"
	}
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(src.path)), ".")
}

func sortItems(items []itemView) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Imported != items[j].Imported {
			return items[i].Imported
		}
		return items[i].Modified.After(items[j].Modified)
	})
}

// ---------- installing ----------

type installResult struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Status  string `json:"status"` // status before the install: new, update, older
	Kind    string `json:"kind"`
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game  string `json:"game"`
		Items []struct {
			Path string `json:"path"`
			Key  string `json:"key"`
		} `json:"items"`
	}
	if err := decode(r, &req); err != nil || len(req.Items) == 0 {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor(req.Game)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if sys.Running(g.Processes()) {
		fail(w, http.StatusConflict, "game_running", nil, "game", g.Name())
		return
	}
	m := games.Manager(loc)
	inst := g.Installed(loc, m)
	tx, err := m.Begin(g.ID(), "install")
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	var done []games.Found
	var results []installResult
	var installedPaths []string
	for _, it := range req.Items {
		if !s.sourceAllowed(it.Path) {
			tx.Rollback()
			fail(w, http.StatusBadRequest, "failed", fmt.Errorf("source not allowed: %s", it.Path))
			return
		}
		f, err := s.installOne(tx, g, loc, inst, m, it.Path, it.Key)
		if err != nil {
			tx.Rollback()
			name := f.Name
			if name == "" {
				name = filepath.Base(it.Path)
			}
			fail(w, http.StatusInternalServerError, errorCode(err), err, "mod", name)
			return
		}
		status, _, _ := g.Status(loc, inst, f, m)
		results = append(results, installResult{Name: f.Name, Version: f.Version, Status: status, Kind: f.Kind})
		done = append(done, f)
		installedPaths = append(installedPaths, it.Path)
		inst = g.Installed(loc, m)
	}
	var names []string
	for _, f := range done {
		names = append(names, f.Name)
	}
	if err := tx.Commit(names); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	for _, f := range done {
		if f.Version != "" {
			_ = m.SetVersion(f.ModID, f.Version)
		}
	}
	// Imported copies are not needed anymore.
	for _, p := range installedPaths {
		if samePath(filepath.Dir(p), s.importsDir()) {
			os.RemoveAll(p)
		}
	}
	writeJSON(w, map[string]any{
		"installed": results,
		"tips":      g.Tips(loc, done),
		"canLaunch": g.CanLaunch(loc),
	})
}

func (s *Server) installOne(tx *install.Tx, g games.Game, loc games.Location, inst []games.Installed, m *install.Manager, p, key string) (games.Found, error) {
	src, err := archive.Open(p)
	if err != nil {
		return games.Found{}, err
	}
	defer src.Close()
	entries, err := src.Entries()
	if err != nil {
		return games.Found{}, err
	}
	var f games.Found
	found := false
	for _, x := range g.Analyze(src, entries) {
		if x.Key == key {
			f, found = x, true
			break
		}
	}
	if !found {
		return games.Found{}, fmt.Errorf("mod %q not found in %s", key, p)
	}
	if f.Unsupported != "" {
		return f, fmt.Errorf("unsupported mod: %s", f.Unsupported)
	}
	plan, err := g.Plan(loc, inst, f)
	if err != nil {
		return f, err
	}
	return f, tx.Apply(src, plan)
}

// ---------- installed mods ----------

func (s *Server) findInstalled(g games.Game, loc games.Location, id string) (games.Installed, bool) {
	for _, it := range g.Installed(loc, games.Manager(loc)) {
		if it.ID == id {
			return it, true
		}
	}
	return games.Installed{}, false
}

func (s *Server) handleInstalled(w http.ResponseWriter, r *http.Request) {
	g, loc, ok := s.gameFor(r.URL.Query().Get("game"))
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	m := games.Manager(loc)
	items := g.Installed(loc, m)
	if items == nil {
		items = []games.Installed{}
	}
	resp := map[string]any{
		"items":     items,
		"location":  loc,
		"canLaunch": g.CanLaunch(loc),
	}
	if h := m.History(); len(h) > 0 {
		resp["last"] = map[string]any{"time": h[0].Time, "action": h[0].Action, "mods": h[0].Mods}
	}
	switch gg := g.(type) {
	case *games.EldenRing:
		pw, installed := gg.CoopPassword(loc)
		resp["coop"] = map[string]any{"installed": installed, "password": pw}
	case *games.Minecraft:
		resp["profiles"] = games.Profiles(loc.Root)
	}
	writeJSON(w, resp)
}

type idRequest struct {
	Game string `json:"game"`
	ID   string `json:"id"`
	On   bool   `json:"on"`
}

func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor(req.Game)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	it, ok := s.findInstalled(g, loc, req.ID)
	if !ok || !it.CanToggle {
		fail(w, http.StatusNotFound, "failed", fmt.Errorf("mod %q not found", req.ID))
		return
	}
	if err := g.SetEnabled(loc, it, req.On); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err, "mod", it.Name)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor(req.Game)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if sys.Running(g.Processes()) {
		fail(w, http.StatusConflict, "game_running", nil, "game", g.Name())
		return
	}
	it, ok := s.findInstalled(g, loc, req.ID)
	if !ok || it.Builtin {
		fail(w, http.StatusNotFound, "failed", fmt.Errorf("mod %q not found", req.ID))
		return
	}
	m := games.Manager(loc)
	tx, err := m.Begin(g.ID(), "remove")
	if err == nil {
		var plan install.Plan
		for _, p := range it.Paths {
			plan.Ops = append(plan.Ops, install.Op{Kind: install.Remove, Dst: p})
		}
		if err = tx.Apply(nil, plan); err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit([]string{it.Name})
		}
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err, "mod", it.Name)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleRepair(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor(req.Game)
	sd, isSD := g.(*games.SevenDays)
	if !ok || !isSD {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	it, ok := s.findInstalled(g, loc, req.ID)
	if !ok {
		fail(w, http.StatusNotFound, "failed", fmt.Errorf("mod %q not found", req.ID))
		return
	}
	src, found, plan, err := sd.RepairPlan(loc, it)
	if err == nil && len(found) == 0 {
		err = errors.New("nothing to repair")
	}
	if err != nil {
		if src != nil {
			src.Close()
		}
		fail(w, http.StatusInternalServerError, errorCode(err), err, "mod", it.Name)
		return
	}
	defer src.Close()
	m := games.Manager(loc)
	tx, err := m.Begin(g.ID(), "repair")
	if err == nil {
		if err = tx.Apply(src, plan); err != nil {
			tx.Rollback()
		} else {
			err = tx.Commit([]string{it.Name})
		}
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err, "mod", it.Name)
		return
	}
	var names []string
	for _, f := range found {
		names = append(names, f.Name)
	}
	writeJSON(w, map[string]any{"ok": true, "mods": names})
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor(req.Game)
	if !ok {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if sys.Running(g.Processes()) {
		fail(w, http.StatusConflict, "game_running", nil, "game", g.Name())
		return
	}
	rec, err := games.Manager(loc).UndoLast()
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "action": rec.Action, "mods": rec.Mods})
}

// ---------- actions ----------

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game string `json:"game"`
		What string `json:"what"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	var target string
	if req.What == "downloads" {
		target = s.Download
	} else {
		_, loc, ok := s.gameFor(req.Game)
		if !ok {
			fail(w, http.StatusNotFound, "not_found", nil)
			return
		}
		switch req.What {
		case "mods":
			target = loc.ModsDir
			_ = os.MkdirAll(target, 0o755)
		case "game":
			target = loc.GameDir
		default:
			target = loc.Root
		}
	}
	if err := sys.Open(target); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req idRequest
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	g, loc, ok := s.gameFor(req.Game)
	if !ok || !g.CanLaunch(loc) {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if err := g.Launch(loc); err != nil {
		fail(w, http.StatusInternalServerError, "launch_failed", err)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleCoopPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	pw := strings.TrimSpace(req.Password)
	if len(pw) > 64 || strings.ContainsAny(pw, "\r\n;#=[]") {
		fail(w, http.StatusBadRequest, "bad_password", nil)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor("eldenring")
	er, isER := g.(*games.EldenRing)
	if !ok || !isER {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if _, installed := er.CoopPassword(loc); !installed {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	if err := er.SetCoopPassword(loc, pw); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "password": pw})
}

func (s *Server) handleFabric(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Req string `json:"req"`
		MC  string `json:"mc"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	g, loc, ok := s.gameFor("minecraft")
	mc, isMC := g.(*games.Minecraft)
	if !ok || !isMC {
		fail(w, http.StatusNotFound, "not_found", nil)
		return
	}
	res, err := mc.SetupFabric(loc, s.client, req.Req, req.MC)
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, res)
}

// handleImport stores a file dropped onto the window. Browsers do not
// reveal the original path, so the file is copied into the data folder.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(filepath.Clean("/" + r.URL.Query().Get("name")))
	if name == "" || name == "." || name == string(filepath.Separator) || !archive.SupportedExt(name) {
		fail(w, http.StatusBadRequest, "unsupported_file", nil, "file", name)
		return
	}
	if err := os.MkdirAll(s.importsDir(), 0o755); err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	dst := filepath.Join(s.importsDir(), name)
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	_, err = io.Copy(f, r.Body)
	f.Close()
	if err == nil {
		err = os.Rename(tmp, dst)
	}
	if err != nil {
		os.Remove(tmp)
		fail(w, http.StatusInternalServerError, errorCode(err), err)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "path": dst})
}

func (s *Server) handleForgetImports(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, http.StatusBadRequest, "failed", err)
		return
	}
	if !samePath(filepath.Dir(req.Path), s.importsDir()) {
		fail(w, http.StatusBadRequest, "failed", nil)
		return
	}
	os.RemoveAll(req.Path)
	writeJSON(w, map[string]any{"ok": true})
}

func (s *Server) handleCover(w http.ResponseWriter, r *http.Request) {
	g := games.ByID(r.PathValue("game"))
	st := steam.Find()
	if g == nil || g.AppID() == 0 || st == nil {
		http.NotFound(w, r)
		return
	}
	p := st.Cover(g.AppID())
	if p == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "max-age=3600")
	http.ServeFile(w, r, p)
}
