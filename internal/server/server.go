// Package server serves the user interface and its JSON API on
// 127.0.0.1. Every API call needs the random token from the start URL, and
// requests from other hosts are refused, so other websites cannot talk to
// the program.
package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/moster803/Any_Install/internal/archive"
	"github.com/moster803/Any_Install/internal/games"
	"github.com/moster803/Any_Install/internal/install"
	"github.com/moster803/Any_Install/internal/steam"
)

// Settings are stored in settings.json in the data folder.
type Settings struct {
	Lang         string            `json:"lang,omitempty"`
	Game         string            `json:"game,omitempty"`
	GamePaths    map[string]string `json:"gamePaths,omitempty"`
	ExtraFolders []string          `json:"extraFolders,omitempty"`
}

// Server is the running program.
type Server struct {
	Version  string
	DataDir  string
	Download string
	Token    string

	web      fs.FS
	mu       sync.Mutex // guards settings
	opMu     sync.Mutex // one install, removal or undo at a time
	settings Settings
	cache    sync.Map // analysis cache
	client   *http.Client

	pingMu   sync.Mutex
	lastPing time.Time
	byeAt    time.Time
	pinged   bool
}

// New creates a server.
func New(version, dataDir, downloads string, web fs.FS) *Server {
	s := &Server{
		Version:  version,
		DataDir:  dataDir,
		Download: downloads,
		Token:    randomToken(),
		web:      web,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
	s.loadSettings()
	return s
}

func randomToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) settingsPath() string { return filepath.Join(s.DataDir, "settings.json") }

func (s *Server) loadSettings() {
	b, err := os.ReadFile(s.settingsPath())
	if err == nil {
		_ = json.Unmarshal(b, &s.settings)
	}
	if s.settings.GamePaths == nil {
		s.settings.GamePaths = map[string]string{}
	}
}

func (s *Server) saveSettings() error {
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(s.settings, "", "  ")
	return os.WriteFile(s.settingsPath(), b, 0o644)
}

func (s *Server) importsDir() string { return filepath.Join(s.DataDir, "imports") }

// env collects what the games need to find themselves.
func (s *Server) env() *games.Env {
	s.mu.Lock()
	custom := map[string]string{}
	for k, v := range s.settings.GamePaths {
		custom[k] = v
	}
	s.mu.Unlock()
	return &games.Env{Steam: steam.Find(), Custom: custom, HTTP: s.client}
}

// Listen starts serving on a random local port and returns the start URL.
func (s *Server) Listen(port int) (string, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	srv := &http.Server{
		Handler:           s.handler(addr),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("server: %v", err)
		}
	}()
	return "http://" + addr + "/?t=" + s.Token, nil
}

func (s *Server) handler(addr string) http.Handler {
	mux := http.NewServeMux()
	api := map[string]http.HandlerFunc{
		"GET /api/ping":            s.handlePing,
		"POST /api/bye":            s.handleBye,
		"GET /api/state":           s.handleState,
		"POST /api/settings":       s.handleSettings,
		"POST /api/game-path":      s.handleGamePath,
		"POST /api/folders":        s.handleFolders,
		"GET /api/scan":            s.handleScan,
		"POST /api/install":        s.handleInstall,
		"GET /api/installed":       s.handleInstalled,
		"POST /api/toggle":         s.handleToggle,
		"POST /api/remove":         s.handleRemove,
		"POST /api/repair":         s.handleRepair,
		"POST /api/undo":           s.handleUndo,
		"POST /api/open":           s.handleOpen,
		"POST /api/launch":         s.handleLaunch,
		"POST /api/coop-password":  s.handleCoopPassword,
		"POST /api/fabric":         s.handleFabric,
		"POST /api/import":         s.handleImport,
		"GET /api/cover/{game}":    s.handleCover,
		"POST /api/forget-imports": s.handleForgetImports,
	}
	for pattern, h := range api {
		mux.HandleFunc(pattern, h)
	}
	mux.Handle("GET /", http.FileServerFS(s.web))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only our own address: protects against DNS rebinding.
		if r.Host != addr && r.Host != strings.Replace(addr, "127.0.0.1", "localhost", 1) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			tok := r.Header.Get("X-Token")
			if tok == "" {
				tok = r.URL.Query().Get("t") // images and sendBeacon cannot set headers
			}
			if tok != s.Token {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		mux.ServeHTTP(w, r)
	})
}

// ---------- lifecycle ----------

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	s.pingMu.Lock()
	s.lastPing = time.Now()
	s.pinged = true
	s.byeAt = time.Time{}
	s.pingMu.Unlock()
	writeJSON(w, map[string]any{"ok": true, "version": s.Version})
}

func (s *Server) handleBye(w http.ResponseWriter, r *http.Request) {
	s.pingMu.Lock()
	s.byeAt = time.Now()
	s.pingMu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// ShouldExit reports whether the window is gone: it said goodbye and did
// not come back (a reload pings again within a second), or it has not
// pinged for a long time. Minimised windows still ping about once a minute.
func (s *Server) ShouldExit(started time.Time) bool {
	s.pingMu.Lock()
	defer s.pingMu.Unlock()
	now := time.Now()
	if !s.byeAt.IsZero() && now.Sub(s.byeAt) > 8*time.Second {
		return true
	}
	if s.pinged && now.Sub(s.lastPing) > 3*time.Minute {
		return true
	}
	return !s.pinged && now.Sub(started) > 3*time.Minute
}

// ---------- helpers ----------

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// apiError is sent to the interface, which shows a translated text for
// Code and Detail as technical information.
type apiError struct {
	Code   string            `json:"error"`
	Detail string            `json:"detail,omitempty"`
	Params map[string]string `json:"params,omitempty"`
}

func fail(w http.ResponseWriter, status int, code string, err error, params ...string) {
	e := apiError{Code: code}
	if err != nil {
		e.Detail = err.Error()
		log.Printf("%s: %v", code, err)
	}
	if len(params) > 1 {
		e.Params = map[string]string{}
		for i := 0; i+1 < len(params); i += 2 {
			e.Params[params[i]] = params[i+1]
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(e)
}

// errorCode maps an error to a code the interface can translate.
func errorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, archive.ErrEncrypted):
		return "encrypted"
	case errors.Is(err, archive.ErrUnsafe):
		return "unsafe"
	case errors.Is(err, install.ErrOutside):
		return "outside"
	case errors.Is(err, install.ErrNothingToUndo):
		return "nothing_to_undo"
	case errors.Is(err, games.ErrLauncherNotRun):
		return "launcher_not_run"
	case errors.Is(err, games.ErrOffline):
		return "offline"
	case errors.Is(err, games.ErrNoVersion):
		return "no_version"
	case errors.Is(err, fs.ErrPermission):
		return "permission"
	case errors.Is(err, fs.ErrExist):
		return "exists"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not a valid zip"), strings.Contains(msg, "unexpected eof"),
		strings.Contains(msg, "checksum"), strings.Contains(msg, "corrupt"), strings.Contains(msg, "bad"):
		return "broken"
	case strings.Contains(msg, "being used by another process"), strings.Contains(msg, "access is denied"):
		return "in_use"
	case strings.Contains(msg, "no space left"), strings.Contains(msg, "not enough space"):
		return "disk_full"
	}
	return "failed"
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

// gameFor locates a game from a request parameter.
func (s *Server) gameFor(id string) (games.Game, games.Location, bool) {
	g := games.ByID(id)
	if g == nil {
		return nil, games.Location{}, false
	}
	loc := g.Locate(s.env())
	return g, loc, loc.Found
}
