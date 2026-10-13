package games

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Fabric is set up the same way the official Fabric installer does it: a
// version file from Fabric's meta server goes into .minecraft/versions and
// a profile is added to the Minecraft Launcher. The launcher downloads
// Minecraft and the libraries itself on the first start.

var launcherProfileFiles = []string{"launcher_profiles.json", "launcher_profiles_microsoft_store.json"}

// ErrLauncherNotRun means the Minecraft Launcher has never been started,
// so there is no profile file to add Fabric to.
var ErrLauncherNotRun = errors.New("minecraft launcher has not been started yet")

// ErrNoVersion means no Minecraft version fits the mods.
var ErrNoVersion = errors.New("no fitting Minecraft version found")

// ErrOffline means Fabric's servers could not be reached.
var ErrOffline = errors.New("fabric servers not reachable")

func fabricMeta() []string {
	if m := os.Getenv("ANYINSTALL_FABRIC_META"); m != "" {
		return []string{strings.TrimSuffix(m, "/") + "/"}
	}
	return []string{"https://meta.fabricmc.net/", "https://meta2.fabricmc.net/", "https://meta3.fabricmc.net/"}
}

func fetchJSON(client *http.Client, p string, v any) error {
	var lastErr error = ErrOffline
	for _, base := range fabricMeta() {
		resp, err := client.Get(base + p)
		if err != nil {
			lastErr = ErrOffline
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("fabric meta %s: %s", p, resp.Status)
			continue
		}
		return json.Unmarshal(b, v)
	}
	return lastErr
}

type gameVersion struct {
	Version string `json:"version"`
	Stable  bool   `json:"stable"`
}

var versionToken = regexp.MustCompile(`\d+(?:\.\d+)+`)

// pickMC chooses the Minecraft version to set up for a mod requirement.
// A version the mod names itself ("~1.21.1" -> 1.21.1) is preferred,
// because neighbouring versions often break mods even when the range
// allows them. Otherwise the newest stable version that fits is used.
func pickMC(req string, versions []gameVersion) string {
	stable := map[string]bool{}
	for _, v := range versions {
		if v.Stable {
			stable[v.Version] = true
		}
	}
	for _, tok := range versionToken.FindAllString(req, -1) {
		if ok, _ := MatchMC(req, tok); ok && stable[tok] {
			return tok
		}
	}
	for _, v := range versions { // newest first
		if ok, _ := MatchMC(req, v.Version); ok && v.Stable {
			return v.Version
		}
	}
	return ""
}

// FabricResult tells the user which profile to pick in the launcher.
type FabricResult struct {
	Profile string `json:"profile"` // name shown in the launcher
	Version string `json:"version"` // version id in .minecraft/versions
	MC      string `json:"mc"`
	Loader  string `json:"loader"`
}

// SetupFabric installs Fabric for the newest stable Minecraft version that
// satisfies req (a mod's requirement, may be empty), or for mc if given.
func (g *Minecraft) SetupFabric(loc Location, client *http.Client, req, mc string) (FabricResult, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	var hasProfiles bool
	for _, f := range launcherProfileFiles {
		hasProfiles = hasProfiles || exists(filepath.Join(loc.Root, f))
	}
	if !hasProfiles {
		return FabricResult{}, ErrLauncherNotRun
	}

	if mc == "" {
		var games []gameVersion
		if err := fetchJSON(client, "v2/versions/game", &games); err != nil {
			return FabricResult{}, err
		}
		mc = pickMC(req, games)
		if mc == "" {
			return FabricResult{}, ErrNoVersion
		}
	}

	var loaders []struct {
		Loader struct {
			Version string `json:"version"`
			Stable  bool   `json:"stable"`
		} `json:"loader"`
	}
	if err := fetchJSON(client, "v2/versions/loader/"+mc, &loaders); err != nil {
		return FabricResult{}, err
	}
	if len(loaders) == 0 {
		return FabricResult{}, ErrNoVersion
	}
	loader := loaders[0].Loader.Version
	for _, l := range loaders {
		if l.Loader.Stable {
			loader = l.Loader.Version
			break
		}
	}

	var profile map[string]any
	if err := fetchJSON(client, "v2/versions/loader/"+mc+"/"+loader+"/profile/json", &profile); err != nil {
		return FabricResult{}, err
	}
	id, _ := profile["id"].(string)
	if id == "" {
		id = "fabric-loader-" + loader + "-" + mc
	}
	vdir := filepath.Join(loc.Root, "versions", id)
	if err := os.MkdirAll(vdir, 0o755); err != nil {
		return FabricResult{}, err
	}
	b, _ := json.Marshal(profile)
	if err := os.WriteFile(filepath.Join(vdir, id+".json"), b, 0o644); err != nil {
		return FabricResult{}, err
	}
	os.Remove(filepath.Join(vdir, id+".jar"))

	name := "fabric-loader-" + mc
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	for _, f := range launcherProfileFiles {
		p := filepath.Join(loc.Root, f)
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var lp map[string]any
		if err := json.Unmarshal(raw, &lp); err != nil {
			return FabricResult{}, fmt.Errorf("%s: %w", f, err)
		}
		profiles, _ := lp["profiles"].(map[string]any)
		if profiles == nil {
			profiles = map[string]any{}
			lp["profiles"] = profiles
		}
		entry, _ := profiles[name].(map[string]any)
		if entry == nil {
			entry = map[string]any{"name": name, "type": "custom", "created": now, "icon": "Furnace"}
			profiles[name] = entry
		}
		entry["lastVersionId"] = id
		entry["lastUsed"] = now
		out, _ := json.MarshalIndent(lp, "", "  ")
		if err := os.WriteFile(p, out, 0o644); err != nil {
			return FabricResult{}, err
		}
	}
	_ = os.MkdirAll(loc.ModsDir, 0o755)
	return FabricResult{Profile: name, Version: id, MC: mc, Loader: loader}, nil
}
