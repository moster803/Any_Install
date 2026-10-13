package archive

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/nwaples/rardecode/v2"
)

// ---------- zip, jar, mrpack ----------

type zipSource struct {
	path string
	kind string
	rc   *zip.ReadCloser
}

func openZip(p, kind string) (Source, error) {
	rc, err := zip.OpenReader(p)
	if err != nil {
		return nil, err
	}
	return &zipSource{path: p, kind: kind, rc: rc}, nil
}

func (z *zipSource) Path() string { return z.path }
func (z *zipSource) Kind() string { return z.kind }
func (z *zipSource) Close() error { return z.rc.Close() }

func (z *zipSource) files() ([]*zip.File, []string, error) {
	var files []*zip.File
	var names []string
	for _, f := range z.rc.File {
		name, ok := CleanName(f.Name)
		if !ok {
			return nil, nil, ErrUnsafe
		}
		if name == "" || Ignored(name) || f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		files = append(files, f)
		names = append(names, name)
	}
	return files, names, nil
}

func (z *zipSource) Entries() ([]Entry, error) {
	files, names, err := z.files()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(files))
	for i, f := range files {
		out = append(out, Entry{Name: names[i], Size: int64(f.UncompressedSize64), Dir: f.FileInfo().IsDir()})
	}
	return addImplicitDirs(out), nil
}

func (z *zipSource) ReadFiles(want []string, maxSize int64) (map[string][]byte, error) {
	files, names, err := z.files()
	if err != nil {
		return nil, err
	}
	w := wanted(want)
	out := map[string][]byte{}
	for i, f := range files {
		if !w[names[i]] || f.FileInfo().IsDir() || int64(f.UncompressedSize64) > maxSize {
			continue
		}
		if f.Flags&0x1 != 0 {
			return nil, ErrEncrypted
		}
		r, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(io.LimitReader(r, maxSize+1))
		r.Close()
		if err != nil {
			return nil, err
		}
		out[names[i]] = b
	}
	return out, nil
}

func (z *zipSource) Extract(prefix, dst string) error {
	files, names, err := z.files()
	if err != nil {
		return err
	}
	for i, f := range files {
		rel, ok := relUnder(names[i], prefix)
		if !ok {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := makeDir(dst, rel); err != nil {
				return err
			}
			continue
		}
		if f.Flags&0x1 != 0 {
			return ErrEncrypted
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		err = writeFile(dst, rel, f.Mode(), r)
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------- 7z ----------

type sevenSource struct {
	path string
	rc   *sevenzip.ReadCloser
}

func open7z(p string) (Source, error) {
	rc, err := sevenzip.OpenReader(p)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "password") {
			return nil, ErrEncrypted
		}
		return nil, err
	}
	return &sevenSource{path: p, rc: rc}, nil
}

func (s *sevenSource) Path() string { return s.path }
func (s *sevenSource) Kind() string { return "7z" }
func (s *sevenSource) Close() error { return s.rc.Close() }

func (s *sevenSource) files() ([]*sevenzip.File, []string, error) {
	var files []*sevenzip.File
	var names []string
	for _, f := range s.rc.File {
		name, ok := CleanName(f.Name)
		if !ok {
			return nil, nil, ErrUnsafe
		}
		if name == "" || Ignored(name) || f.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		files = append(files, f)
		names = append(names, name)
	}
	return files, names, nil
}

func (s *sevenSource) Entries() ([]Entry, error) {
	files, names, err := s.files()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(files))
	for i, f := range files {
		out = append(out, Entry{Name: names[i], Size: int64(f.UncompressedSize), Dir: f.FileInfo().IsDir()})
	}
	return addImplicitDirs(out), nil
}

func (s *sevenSource) ReadFiles(want []string, maxSize int64) (map[string][]byte, error) {
	files, names, err := s.files()
	if err != nil {
		return nil, err
	}
	w := wanted(want)
	out := map[string][]byte{}
	for i, f := range files {
		if !w[names[i]] || f.FileInfo().IsDir() || int64(f.UncompressedSize) > maxSize {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return nil, wrap7zErr(err)
		}
		b, err := io.ReadAll(io.LimitReader(r, maxSize+1))
		r.Close()
		if err != nil {
			return nil, wrap7zErr(err)
		}
		out[names[i]] = b
	}
	return out, nil
}

func (s *sevenSource) Extract(prefix, dst string) error {
	files, names, err := s.files()
	if err != nil {
		return err
	}
	for i, f := range files {
		rel, ok := relUnder(names[i], prefix)
		if !ok {
			continue
		}
		if f.FileInfo().IsDir() {
			if err := makeDir(dst, rel); err != nil {
				return err
			}
			continue
		}
		r, err := f.Open()
		if err != nil {
			return wrap7zErr(err)
		}
		err = writeFile(dst, rel, f.Mode(), r)
		r.Close()
		if err != nil {
			return wrap7zErr(err)
		}
	}
	return nil
}

func wrap7zErr(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "password") {
		return ErrEncrypted
	}
	return err
}

// ---------- rar ----------

// rarSource reads rar archives sequentially, because solid archives do not
// allow random access.
type rarSource struct {
	path string
}

func openRar(p string) (Source, error) {
	rc, err := rardecode.OpenReader(p)
	if err != nil {
		return nil, wrapRarErr(err)
	}
	rc.Close()
	return &rarSource{path: p}, nil
}

func (r *rarSource) Path() string { return r.path }
func (r *rarSource) Kind() string { return "rar" }
func (r *rarSource) Close() error { return nil }

// walk calls fn for every safe entry. fn may read from rd.
func (r *rarSource) walk(fn func(name string, h *rardecode.FileHeader, rd io.Reader) error) error {
	rc, err := rardecode.OpenReader(r.path)
	if err != nil {
		return wrapRarErr(err)
	}
	defer rc.Close()
	for {
		h, err := rc.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return wrapRarErr(err)
		}
		if h.Encrypted || h.HeaderEncrypted {
			return ErrEncrypted
		}
		name, ok := CleanName(h.Name)
		if !ok {
			return ErrUnsafe
		}
		if name == "" || Ignored(name) || h.Mode()&fs.ModeSymlink != 0 {
			continue
		}
		if err := fn(name, h, rc); err != nil {
			return err
		}
	}
}

func (r *rarSource) Entries() ([]Entry, error) {
	var out []Entry
	err := r.walk(func(name string, h *rardecode.FileHeader, _ io.Reader) error {
		out = append(out, Entry{Name: name, Size: h.UnPackedSize, Dir: h.IsDir})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return addImplicitDirs(out), nil
}

func (r *rarSource) ReadFiles(want []string, maxSize int64) (map[string][]byte, error) {
	w := wanted(want)
	out := map[string][]byte{}
	err := r.walk(func(name string, h *rardecode.FileHeader, rd io.Reader) error {
		if !w[name] || h.IsDir || h.UnPackedSize > maxSize {
			return nil
		}
		b, err := io.ReadAll(io.LimitReader(rd, maxSize+1))
		if err != nil {
			return err
		}
		out[name] = b
		return nil
	})
	return out, err
}

func (r *rarSource) Extract(prefix, dst string) error {
	return r.walk(func(name string, h *rardecode.FileHeader, rd io.Reader) error {
		rel, ok := relUnder(name, prefix)
		if !ok {
			return nil
		}
		if h.IsDir {
			return makeDir(dst, rel)
		}
		return writeFile(dst, rel, h.Mode(), rd)
	})
}

func wrapRarErr(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "password") {
		return ErrEncrypted
	}
	return err
}

// ---------- plain folders ----------

// dirSource treats an already unpacked folder like an archive whose top
// entry is the folder itself, so "Downloads/MyMod/ModInfo.xml" becomes
// "MyMod/ModInfo.xml".
type dirSource struct {
	root string
}

// maxDirEntries keeps scanning of huge folders (a whole game, a photo
// collection) cheap. Mod folders are far smaller.
const maxDirEntries = 20000

var errTooBig = errors.New("folder too big")

func (d *dirSource) Path() string { return d.root }
func (d *dirSource) Kind() string { return "dir" }
func (d *dirSource) Close() error { return nil }

func (d *dirSource) base() string { return filepath.Base(d.root) }

func (d *dirSource) Entries() ([]Entry, error) {
	base := d.base()
	out := []Entry{{Name: base, Dir: true}}
	err := filepath.WalkDir(d.root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if p == d.root {
			return nil
		}
		if de.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(d.root, p)
		name := base + "/" + filepath.ToSlash(rel)
		if Ignored(name) {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		var size int64
		if !de.IsDir() {
			if info, err := de.Info(); err == nil {
				size = info.Size()
			}
		}
		out = append(out, Entry{Name: name, Size: size, Dir: de.IsDir()})
		if len(out) > maxDirEntries {
			return errTooBig
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (d *dirSource) local(name string) (string, bool) {
	rel, ok := relUnder(name, d.base())
	if !ok {
		return "", false
	}
	if rel == "" {
		return d.root, true
	}
	p, err := SafeJoin(d.root, rel)
	if err != nil {
		return "", false
	}
	return p, true
}

func (d *dirSource) ReadFiles(want []string, maxSize int64) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, n := range want {
		p, ok := d.local(n)
		if !ok {
			continue
		}
		st, err := os.Lstat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > maxSize {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		out[n] = b
	}
	return out, nil
}

func (d *dirSource) Extract(prefix, dst string) error {
	if prefix == "" {
		// Everything means the folder itself, including its name, just
		// like the entry names returned by Entries.
		return copyTree(d.root, filepath.Join(dst, d.base()))
	}
	src, ok := d.local(prefix)
	if !ok {
		return os.ErrNotExist
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !st.IsDir() {
		f, err := os.Open(src)
		if err != nil {
			return err
		}
		defer f.Close()
		return writeFile(dst, "", st.Mode(), f)
	}
	return copyTree(src, dst)
}

// copyTree copies a folder recursively, skipping symlinks and OS junk.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		rel, _ := filepath.Rel(src, p)
		if Ignored(filepath.ToSlash(rel)) {
			if de.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if de.IsDir() {
			return makeDir(dst, filepath.ToSlash(rel))
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		return writeFile(dst, filepath.ToSlash(rel), info.Mode(), f)
	})
}

// CopyFile copies a single file (used for jars and resource packs that are
// installed as they are).
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// CopyTree is exported for the installer's cross-drive fallback.
func CopyTree(src, dst string) error { return copyTree(src, dst) }
