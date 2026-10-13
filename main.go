// Any Install finds game mods in the download folder and installs them in
// the right place. Run it, and a window opens.
package main

import (
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/moster803/Any_Install/internal/demo"
	"github.com/moster803/Any_Install/internal/paths"
	"github.com/moster803/Any_Install/internal/server"
	"github.com/moster803/Any_Install/internal/sys"
	"github.com/moster803/Any_Install/internal/window"
)

// version is set when building a release.
var version = "dev"

//go:embed web
var webFS embed.FS

type instance struct {
	URL   string `json:"url"`
	Token string `json:"token"`
	PID   int    `json:"pid"`
}

func main() {
	port := flag.Int("port", 0, "port for the local server (0 = random)")
	noWindow := flag.Bool("no-window", false, "do not open a window, only print the address")
	showVersion := flag.Bool("version", false, "print the version")
	demoMode := flag.Bool("demo", false, "try the program with example games and mods (nothing real is changed)")
	flag.Parse()
	if *showVersion {
		fmt.Println("Any Install", version)
		return
	}
	if *noWindow {
		os.Setenv("ANYINSTALL_NO_BROWSER", "1")
	}
	if *demoMode {
		root := filepath.Join(os.TempDir(), "AnyInstall-Demo")
		os.RemoveAll(root)
		env, err := demo.Build(root)
		if err != nil {
			sys.MessageBox("Any Install", "Demo: "+err.Error())
			os.Exit(1)
		}
		for k, v := range env {
			os.Setenv(k, v)
		}
		fmt.Println("Demo:", root)
	}

	dataDir := paths.DataDir()
	setupLog(dataDir)
	log.Printf("Any Install %s starting, data in %s", version, dataDir)

	// Already running? Then just show the existing window again.
	instFile := filepath.Join(dataDir, "instance.json")
	if inst, ok := running(instFile); ok {
		log.Printf("already running at %s", inst.URL)
		if _, err := window.Open(inst.URL); err != nil {
			sys.MessageBox("Any Install", "Any Install läuft bereits: "+inst.URL)
		}
		return
	}

	web, _ := fs.Sub(webFS, "web")
	srv := server.New(version, dataDir, paths.DownloadsDir(), web)
	url, err := srv.Listen(*port)
	if err != nil {
		log.Printf("listen: %v", err)
		sys.MessageBox("Any Install", "Any Install konnte nicht starten:\n"+err.Error())
		os.Exit(1)
	}
	b, _ := json.Marshal(instance{URL: url, Token: srv.Token, PID: os.Getpid()})
	_ = os.WriteFile(instFile, b, 0o600)
	defer os.Remove(instFile)

	fmt.Println("Any Install läuft / is running:", url)
	log.Printf("listening on %s", url)
	started := time.Now()
	if _, err := window.Open(url); err != nil {
		log.Printf("window: %v", err)
		sys.MessageBox("Any Install", "Bitte öffne diese Adresse im Browser / Please open in your browser:\n"+url)
	}

	if os.Getenv("ANYINSTALL_NO_BROWSER") != "" {
		select {} // headless mode for tests: run until killed
	}
	for !srv.ShouldExit(started) {
		time.Sleep(2 * time.Second)
	}
	log.Printf("window closed, exiting")
}

// running checks whether another instance answers.
func running(instFile string) (instance, bool) {
	var inst instance
	b, err := os.ReadFile(instFile)
	if err != nil || json.Unmarshal(b, &inst) != nil || inst.URL == "" {
		return inst, false
	}
	base := inst.URL
	if i := len(base) - len("/?t="+inst.Token); i > 0 {
		base = base[:i]
	}
	req, _ := http.NewRequest("GET", base+"/api/ping", nil)
	req.Header.Set("X-Token", inst.Token)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return inst, false
	}
	resp.Body.Close()
	return inst, resp.StatusCode == http.StatusOK
}

// setupLog writes a small log file that can be sent along with a problem
// report.
func setupLog(dataDir string) {
	p := filepath.Join(dataDir, "anyinstall.log")
	if st, err := os.Stat(p); err == nil && st.Size() > 1<<20 {
		os.Rename(p, p+".old")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	log.SetOutput(io.MultiWriter(f, os.Stderr))
}
