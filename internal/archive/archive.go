// Package archive gives read access to mod downloads: zip, 7z and rar
// archives, Minecraft jars and plain folders. All formats share one small
// interface, and every write path is checked so that no file can end up
// outside the destination folder.
package archive

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Entry is one file or folder inside a source. Name always uses "/" and
// never starts with "/" or contains "..".
type Entry struct {
	Name string
	Size int64
	Dir  bool
}

// Base returns the last element of the entry name.
func (e Entry) Base() string { return path.Base(e.Name) }

// Source is a downloaded mod: an archive or a folder.
type Source interface {
	// Path is the location on disk.
	Path() string
	// Kind is one of "zip", "7z", "rar", "jar", "mrpack" or "dir".
	Kind() string
	// Entries lists every file and folder (folders implied by file paths
	// are included).
	Entries() ([]Entry, error)
	// ReadFiles returns the content of the named files. Files bigger than
	// maxSize are skipped. Missing files are simply absent from the map.
	ReadFiles(names []string, maxSize int64) (map[string][]byte, error)
	// Extract writes every file below prefix ("" = everything) into dst,
	// with prefix removed from the path. If prefix names a single file,
	// that file is written to dst itself.
	Extract(prefix, dst string) error
	Close() error
}

// ErrUnsafe is returned for archives with entries that try to escape the
// destination folder (absolute paths, "..", drive letters).
var ErrUnsafe = errors.New("archive contains unsafe paths")

// ErrEncrypted is returned for password protected archives.
var ErrEncrypted = errors.New("archive is password protected")

// SupportedExt reports whether a file extension can be opened.
func SupportedExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".zip", ".7z", ".rar", ".jar", ".mrpack":
		return true
	}
	return ModFileExt(name)
}

// ModFileExt reports whether a file is a mod by itself, without an
// archive around it: Unreal Engine paks, plugin DLLs, truck simulator
// .scs files and Sims 4 packages and scripts.
func ModFileExt(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".pak", ".utoc", ".ucas", ".dll", ".scs", ".package", ".ts4script":
		return true
	}
	return false
}

// Open opens a file or folder as a Source.
func Open(p string) (Source, error) {
	st, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return &dirSource{root: p}, nil
	}
	ext := strings.ToLower(filepath.Ext(p))
	switch ext {
	case ".zip", ".jar", ".mrpack":
		return openZip(p, strings.TrimPrefix(ext, "."))
	case ".7z":
		return open7z(p)
	case ".rar":
		return openRar(p)
	}
	if ModFileExt(p) {
		return &fileSource{path: p, size: st.Size()}, nil
	}
	return nil, fmt.Errorf("unsupported file type %q", ext)
}

// CleanName normalises an entry name from an archive. It returns ok=false
// for names that are unsafe to extract.
func CleanName(name string) (string, bool) {
	n := strings.ReplaceAll(name, "\\", "/")
	if n == "" {
		return "", false
	}
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return "", false
	}
	n = strings.TrimSuffix(n, "/")
	for _, part := range strings.Split(n, "/") {
		if part == ".." {
			return "", false
		}
	}
	n = path.Clean(n)
	if n == "." || n == "" {
		return "", true // the archive root itself, harmless but no entry
	}
	if strings.HasPrefix(n, "../") || n == ".." {
		return "", false
	}
	return n, true
}

// Ignored reports whether an entry is OS junk that should never be
// installed or used for detection.
func Ignored(name string) bool {
	for _, part := range strings.Split(name, "/") {
		switch strings.ToLower(part) {
		case "__macosx", ".ds_store", "thumbs.db", "desktop.ini":
			return true
		}
	}
	return false
}

// SafeJoin joins an archive-relative name onto dst and makes sure the
// result stays inside dst.
func SafeJoin(dst, rel string) (string, error) {
	clean, ok := CleanName(rel)
	if !ok || clean == "" {
		return "", ErrUnsafe
	}
	full := filepath.Join(dst, filepath.FromSlash(clean))
	r, err := filepath.Rel(dst, full)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", ErrUnsafe
	}
	return full, nil
}

// relUnder returns the part of name below prefix. ok is false if name is
// not below prefix.
func relUnder(name, prefix string) (string, bool) {
	if prefix == "" {
		return name, true
	}
	if name == prefix {
		return "", true
	}
	if strings.HasPrefix(name, prefix+"/") {
		return name[len(prefix)+1:], true
	}
	return "", false
}

// addImplicitDirs completes a list of entries with the parent folders of
// every entry, because many archives do not store folder entries.
func addImplicitDirs(entries []Entry) []Entry {
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Name] = true
	}
	out := entries
	for _, e := range entries {
		dir := path.Dir(e.Name)
		for dir != "." && dir != "/" && !seen[dir] {
			seen[dir] = true
			out = append(out, Entry{Name: dir, Dir: true})
			dir = path.Dir(dir)
		}
	}
	return out
}

// writeFile extracts one entry to disk below dst. An empty rel means the
// entry itself was requested, and it is written to dst directly.
func writeFile(dst, rel string, mode fs.FileMode, r io.Reader) error {
	full := dst
	if rel != "" {
		var err error
		if full, err = SafeJoin(dst, rel); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	perm := fs.FileMode(0o644)
	// Start scripts of Linux mod loaders (start_game_bepinex.sh) often
	// lose their executable flag in zips made on Windows.
	if mode&0o111 != 0 || strings.EqualFold(filepath.Ext(full), ".sh") {
		perm = 0o755
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func makeDir(dst, rel string) error {
	if rel == "" {
		return os.MkdirAll(dst, 0o755)
	}
	full, err := SafeJoin(dst, rel)
	if err != nil {
		return err
	}
	return os.MkdirAll(full, 0o755)
}

func wanted(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
