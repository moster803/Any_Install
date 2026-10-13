package games

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// JarMeta is what a Minecraft mod jar says about itself.
type JarMeta struct {
	Loader  string   // fabric, quilt, forge, neoforge
	ID      string   // mod id
	Name    string   // display name
	Version string   // mod version
	MC      string   // required Minecraft version(s), as written by the mod
	Deps    []string // other required mods
}

// jarMetaFiles are the files that identify a mod jar.
var jarMetaFiles = []string{
	"fabric.mod.json", "quilt.mod.json",
	"META-INF/neoforge.mods.toml", "META-INF/mods.toml",
	"META-INF/MANIFEST.MF",
}

// ParseJarFiles reads the metadata files of a jar. ok is false if the jar
// is not a mod.
func ParseJarFiles(files map[string][]byte) (JarMeta, bool) {
	if b, ok := files["quilt.mod.json"]; ok {
		if m, ok := parseQuilt(b); ok {
			return m, true
		}
	}
	if b, ok := files["fabric.mod.json"]; ok {
		if m, ok := parseFabric(b); ok {
			return m, true
		}
	}
	manifest := string(files["META-INF/MANIFEST.MF"])
	if b, ok := files["META-INF/neoforge.mods.toml"]; ok {
		if m, ok := parseModsToml(b, "neoforge", manifest); ok {
			return m, true
		}
	}
	if b, ok := files["META-INF/mods.toml"]; ok {
		if m, ok := parseModsToml(b, "forge", manifest); ok {
			return m, true
		}
	}
	return JarMeta{}, false
}

// ParseJarBytes reads a jar that is stored inside another archive.
func ParseJarBytes(b []byte) (JarMeta, bool) {
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		return JarMeta{}, false
	}
	files := map[string][]byte{}
	want := wanted(jarMetaFiles)
	for _, f := range zr.File {
		if !want[f.Name] || f.UncompressedSize64 > 4<<20 {
			continue
		}
		r, err := f.Open()
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(r, 4<<20))
		r.Close()
		files[f.Name] = data
	}
	return ParseJarFiles(files)
}

func wanted(names []string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

// lenientJSON removes raw line breaks and tabs inside strings, which some
// fabric.mod.json files contain and which the Fabric loader accepts.
func lenientJSON(b []byte, v any) error {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if err := json.Unmarshal(b, v); err == nil {
		return nil
	}
	var out bytes.Buffer
	in := false
	esc := false
	for _, c := range b {
		switch {
		case esc:
			esc = false
		case c == '\\' && in:
			esc = true
		case c == '"':
			in = !in
		case in && (c == '\n' || c == '\r' || c == '\t'):
			c = ' '
		}
		out.WriteByte(c)
	}
	return json.Unmarshal(out.Bytes(), v)
}

// stringOrList accepts "x" or ["x", "y"] and joins lists with " || ".
func stringOrList(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return strings.Join(list, " || ")
	}
	return ""
}

func cleanVersion(v string) string {
	if strings.Contains(v, "${") {
		return ""
	}
	return strings.TrimSpace(v)
}

func parseFabric(b []byte) (JarMeta, bool) {
	var j struct {
		ID      string                     `json:"id"`
		Name    string                     `json:"name"`
		Version string                     `json:"version"`
		Depends map[string]json.RawMessage `json:"depends"`
	}
	if lenientJSON(b, &j) != nil || j.ID == "" {
		return JarMeta{}, false
	}
	m := JarMeta{Loader: "fabric", ID: j.ID, Name: j.Name, Version: cleanVersion(j.Version)}
	for k, v := range j.Depends {
		switch k {
		case "minecraft":
			m.MC = stringOrList(v)
		case "java", "fabricloader", "fabric-loader":
		default:
			m.Deps = append(m.Deps, k)
		}
	}
	sort.Strings(m.Deps)
	if m.Name == "" {
		m.Name = m.ID
	}
	return m, true
}

func parseQuilt(b []byte) (JarMeta, bool) {
	var j struct {
		Q struct {
			ID       string `json:"id"`
			Version  string `json:"version"`
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Depends []json.RawMessage `json:"depends"`
		} `json:"quilt_loader"`
	}
	if lenientJSON(b, &j) != nil || j.Q.ID == "" {
		return JarMeta{}, false
	}
	m := JarMeta{Loader: "quilt", ID: j.Q.ID, Name: j.Q.Metadata.Name, Version: cleanVersion(j.Q.Version)}
	for _, d := range j.Q.Depends {
		var id string
		var versions json.RawMessage
		if json.Unmarshal(d, &id) != nil {
			var obj struct {
				ID       string          `json:"id"`
				Versions json.RawMessage `json:"versions"`
			}
			if json.Unmarshal(d, &obj) != nil {
				continue
			}
			id, versions = obj.ID, obj.Versions
		}
		switch id {
		case "minecraft":
			if len(versions) > 0 {
				m.MC = stringOrList(versions)
			}
		case "java", "quilt_loader", "":
		default:
			m.Deps = append(m.Deps, id)
		}
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m, true
}

var tomlModID = regexp.MustCompile(`(?m)^\s*modId\s*=\s*"([^"]+)"`)
var tomlDisplay = regexp.MustCompile(`(?m)^\s*displayName\s*=\s*"([^"]+)"`)

func parseModsToml(b []byte, loader, manifest string) (JarMeta, bool) {
	var t struct {
		Mods []struct {
			ModID       string `toml:"modId"`
			Version     string `toml:"version"`
			DisplayName string `toml:"displayName"`
		} `toml:"mods"`
		Dependencies map[string][]struct {
			ModID        string `toml:"modId"`
			VersionRange string `toml:"versionRange"`
		} `toml:"dependencies"`
	}
	m := JarMeta{Loader: loader}
	if _, err := toml.Decode(string(b), &t); err == nil && len(t.Mods) > 0 {
		mod := t.Mods[0]
		m.ID, m.Name, m.Version = mod.ModID, mod.DisplayName, cleanVersion(mod.Version)
		for _, d := range t.Dependencies[mod.ModID] {
			switch d.ModID {
			case "minecraft":
				m.MC = d.VersionRange
			case "neoforge":
				m.Loader = "neoforge"
			case "forge", "java":
			default:
				m.Deps = append(m.Deps, d.ModID)
			}
		}
	} else {
		// Some files are not valid TOML; the id is still enough.
		if x := tomlModID.FindStringSubmatch(string(b)); x != nil {
			m.ID = x[1]
		}
		if x := tomlDisplay.FindStringSubmatch(string(b)); x != nil {
			m.Name = x[1]
		}
	}
	if m.ID == "" {
		return JarMeta{}, false
	}
	if m.Version == "" {
		for _, line := range strings.Split(manifest, "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(line), "Implementation-Version:"); ok {
				m.Version = strings.TrimSpace(v)
			}
		}
	}
	if m.Name == "" {
		m.Name = m.ID
	}
	return m, true
}

// MatchMC reports whether a Minecraft version satisfies a requirement.
// known is false if the requirement could not be understood (then the mod
// is assumed to fit).
func MatchMC(req, version string) (ok, known bool) {
	req = strings.TrimSpace(req)
	if req == "" || req == "*" {
		return true, false
	}
	if strings.HasPrefix(req, "[") || strings.HasPrefix(req, "(") {
		return matchMaven(req, version)
	}
	for _, alt := range strings.Split(req, "||") {
		fields := strings.Fields(alt)
		if len(fields) == 0 {
			continue
		}
		all := true
		for _, f := range fields {
			if !matchPredicate(f, version) {
				all = false
				break
			}
		}
		if all {
			return true, true
		}
	}
	return false, true
}

func matchPredicate(p, v string) bool {
	switch {
	case p == "*":
		return true
	case strings.HasPrefix(p, ">="):
		return CompareVersions(v, p[2:]) >= 0
	case strings.HasPrefix(p, "<="):
		return CompareVersions(v, p[2:]) <= 0
	case strings.HasPrefix(p, ">"):
		return CompareVersions(v, p[1:]) > 0
	case strings.HasPrefix(p, "<"):
		return CompareVersions(v, p[1:]) < 0
	case strings.HasPrefix(p, "="):
		return CompareVersions(v, p[1:]) == 0
	case strings.HasPrefix(p, "~"):
		base := p[1:]
		return CompareVersions(v, base) >= 0 && CompareVersions(v, bump(base, 1)) < 0
	case strings.HasPrefix(p, "^"):
		base := p[1:]
		return CompareVersions(v, base) >= 0 && CompareVersions(v, bump(base, 0)) < 0
	case strings.HasSuffix(p, ".x") || strings.HasSuffix(p, ".*") || strings.HasSuffix(p, ".X"):
		prefix := p[:len(p)-2]
		return v == prefix || strings.HasPrefix(v, prefix+".")
	}
	return CompareVersions(v, p) == 0
}

// bump increases the version part at index i and drops the rest:
// bump("1.21.1", 1) = "1.22".
func bump(v string, i int) string {
	parts := strings.Split(v, ".")
	for len(parts) <= i {
		parts = append(parts, "0")
	}
	n := 0
	for _, c := range parts[i] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	parts = parts[:i+1]
	parts[i] = itoa(n + 1)
	return strings.Join(parts, ".")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// matchMaven handles Forge style ranges like "[1.20.1,1.21)".
func matchMaven(req, v string) (bool, bool) {
	known := false
	rest := req
	for {
		start := strings.IndexAny(rest, "[(")
		if start < 0 {
			break
		}
		end := strings.IndexAny(rest[start:], "])")
		if end < 0 {
			break
		}
		end += start
		known = true
		lowIncl := rest[start] == '['
		highIncl := rest[end] == ']'
		body := rest[start+1 : end]
		rest = rest[end+1:]
		lo, hi, hasComma := strings.Cut(body, ",")
		lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)
		if !hasComma {
			if CompareVersions(v, lo) == 0 {
				return true, true
			}
			continue
		}
		ok := true
		if lo != "" {
			c := CompareVersions(v, lo)
			ok = c > 0 || (c == 0 && lowIncl)
		}
		if ok && hi != "" {
			c := CompareVersions(v, hi)
			ok = c < 0 || (c == 0 && highIncl)
		}
		if ok {
			return true, true
		}
	}
	return false, known
}

// DescribeMC turns a requirement into a short text for the user, for
// example "~1.21.1" becomes "1.21.1+" and "[1.20.1,1.21)" "1.20.1 – 1.21".
func DescribeMC(req string) string {
	req = strings.TrimSpace(req)
	switch {
	case req == "" || req == "*":
		return ""
	case strings.HasPrefix(req, "~") && !strings.ContainsAny(req, " |"):
		return req[1:] + "+"
	case strings.HasPrefix(req, ">=") && !strings.ContainsAny(req, " |"):
		return req[2:] + "+"
	case strings.HasPrefix(req, "[") && strings.Count(req, "[")+strings.Count(req, "(") == 1:
		lo, hi, _ := strings.Cut(strings.Trim(req, "[]()"), ",")
		lo, hi = strings.TrimSpace(lo), strings.TrimSpace(hi)
		switch {
		case lo != "" && hi != "":
			return lo + " – " + hi
		case lo != "":
			return lo + "+"
		}
	}
	return req
}
