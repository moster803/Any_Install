// Package install applies install plans as transactions. Everything that
// gets replaced or removed is moved into a backup folder first, so every
// transaction can be undone.
package install

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moster803/Any_Install/internal/archive"
)

// OpKind says what an Op does.
type OpKind string

const (
	// ReplaceDir extracts the source folder Src into Dst. An existing Dst
	// is moved to the backup first.
	ReplaceDir OpKind = "replace-dir"
	// MergeDir extracts Src into Dst file by file. Only files that exist
	// in both are backed up and replaced.
	MergeDir OpKind = "merge-dir"
	// PutFile extracts the single file Src to Dst.
	PutFile OpKind = "put-file"
	// PutSelf copies the downloaded file itself (a jar or a resource pack)
	// to Dst.
	PutSelf OpKind = "put-self"
	// Remove moves Dst into the backup.
	Remove OpKind = "remove"
)

// Op is one step of a plan. Src is a name inside the source, Dst a full
// path on disk.
type Op struct {
	Kind OpKind
	Src  string
	Dst  string
}

// Plan is everything needed to install one mod.
type Plan struct {
	Ops []Op
	// KeepIni lists ini files whose existing values should survive an
	// update (for example the Seamless Co-op password).
	KeepIni []string
}

// Entry records one change so it can be undone.
type Entry struct {
	Dst        string `json:"dst"`
	Backup     string `json:"backup,omitempty"` // relative to the record folder
	CreatedDir bool   `json:"createdDir,omitempty"`
}

// Record describes a finished transaction.
type Record struct {
	ID      string    `json:"id"`
	Game    string    `json:"game"`
	Action  string    `json:"action"` // "install" or "remove"
	Time    time.Time `json:"time"`
	Mods    []string  `json:"mods"`
	Entries []Entry   `json:"entries"`
}

// Manager keeps the backups of one game.
type Manager struct {
	// Root is the backup folder, e.g. "<mods parent>/AnyInstall-Backup".
	Root string
	// Allowed lists the folders the manager may change. Any path outside
	// of them is refused, as a last line of defence.
	Allowed []string
	// Keep is how many transactions are kept for undo.
	Keep int
}

// ErrOutside is returned when a plan would touch a path outside of the
// game's folders.
var ErrOutside = errors.New("path outside of the game folders")

// ErrNothingToUndo is returned by UndoLast when there is no backup.
var ErrNothingToUndo = errors.New("nothing to undo")

func (m *Manager) check(p string) error {
	clean := filepath.Clean(p)
	for _, a := range m.Allowed {
		a = filepath.Clean(a)
		rel, err := filepath.Rel(a, clean)
		if err != nil || rel == "." {
			continue
		}
		if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrOutside, p)
}

// Tx is a running transaction.
type Tx struct {
	m       *Manager
	rec     Record
	dir     string
	staging string
	counter int
}

func newID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return time.Now().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Begin starts a transaction.
func (m *Manager) Begin(game, action string) (*Tx, error) {
	id := newID()
	tx := &Tx{m: m, rec: Record{ID: id, Game: game, Action: action, Time: time.Now()}}
	tx.dir = filepath.Join(m.Root, id)
	tx.staging = filepath.Join(m.Root, ".staging-"+id)
	if err := os.MkdirAll(tx.dir, 0o755); err != nil {
		return nil, err
	}
	return tx, nil
}

func (tx *Tx) next(base string) string {
	tx.counter++
	return fmt.Sprintf("%d-%s", tx.counter, base)
}

// backup moves p into the record folder and records it.
func (tx *Tx) backup(p string) error {
	if err := tx.m.check(p); err != nil {
		return err
	}
	rel := filepath.Join("files", tx.next(filepath.Base(p)))
	dst := filepath.Join(tx.dir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := move(p, dst); err != nil {
		return err
	}
	tx.rec.Entries = append(tx.rec.Entries, Entry{Dst: p, Backup: filepath.ToSlash(rel)})
	return nil
}

// place moves a staged file or folder to dst, backing up whatever was
// there before.
func (tx *Tx) place(staged, dst string) error {
	if err := tx.m.check(dst); err != nil {
		return err
	}
	// Parent folders first, so that undo (which runs backwards) removes
	// the file before it tries to remove the then empty folders.
	if err := tx.mkdirs(filepath.Dir(dst)); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		// On undo the new content is deleted and this backup is put back.
		if err := tx.backup(dst); err != nil {
			return err
		}
	} else {
		tx.rec.Entries = append(tx.rec.Entries, Entry{Dst: dst})
	}
	return move(staged, dst)
}

// mkdirs creates missing parent folders and records them, so that undo
// can remove them again if they end up empty.
func (tx *Tx) mkdirs(dir string) error {
	var missing []string
	for d := dir; ; d = filepath.Dir(d) {
		if _, err := os.Stat(d); err == nil {
			break
		}
		missing = append(missing, d)
		if filepath.Dir(d) == d {
			break
		}
	}
	for i := len(missing) - 1; i >= 0; i-- {
		d := missing[i]
		if err := tx.m.check(d); err != nil {
			return err
		}
		if err := os.Mkdir(d, 0o755); err != nil && !os.IsExist(err) {
			return err
		}
		tx.rec.Entries = append(tx.rec.Entries, Entry{Dst: d, CreatedDir: true})
	}
	return nil
}

func (tx *Tx) stage() (string, error) {
	tx.counter++
	d := filepath.Join(tx.staging, fmt.Sprint(tx.counter))
	return d, os.MkdirAll(filepath.Dir(d), 0o755)
}

// Apply runs a plan against a source.
func (tx *Tx) Apply(src archive.Source, plan Plan) error {
	kept := map[string]map[string]string{}
	for _, ini := range plan.KeepIni {
		if b, err := os.ReadFile(ini); err == nil {
			kept[ini] = ParseIni(string(b))
		}
	}
	for _, op := range plan.Ops {
		if err := tx.apply(src, op); err != nil {
			return err
		}
	}
	for ini, old := range kept {
		b, err := os.ReadFile(ini)
		if err != nil {
			continue
		}
		merged := MergeIni(string(b), old)
		if err := os.WriteFile(ini, []byte(merged), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (tx *Tx) apply(src archive.Source, op Op) error {
	if err := tx.m.check(op.Dst); err != nil {
		return err
	}
	switch op.Kind {
	case Remove:
		if _, err := os.Lstat(op.Dst); err != nil {
			return nil
		}
		return tx.backup(op.Dst)

	case ReplaceDir:
		st, err := tx.stage()
		if err != nil {
			return err
		}
		if err := src.Extract(op.Src, st); err != nil {
			return err
		}
		if _, err := os.Stat(st); err != nil {
			if err := os.MkdirAll(st, 0o755); err != nil {
				return err
			}
		}
		return tx.place(st, op.Dst)

	case PutFile:
		st, err := tx.stage()
		if err != nil {
			return err
		}
		if err := src.Extract(op.Src, st); err != nil {
			return err
		}
		if info, err := os.Stat(st); err != nil || info.IsDir() {
			return fmt.Errorf("file %s not found in download", op.Src)
		}
		return tx.place(st, op.Dst)

	case PutSelf:
		st, err := tx.stage()
		if err != nil {
			return err
		}
		if err := archive.CopyFile(src.Path(), st); err != nil {
			return err
		}
		return tx.place(st, op.Dst)

	case MergeDir:
		st, err := tx.stage()
		if err != nil {
			return err
		}
		if err := src.Extract(op.Src, st); err != nil {
			return err
		}
		var files []string
		err = filepath.WalkDir(st, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return err
		}
		for _, f := range files {
			rel, _ := filepath.Rel(st, f)
			if err := tx.place(f, filepath.Join(op.Dst, rel)); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("unknown operation %q", op.Kind)
}

// Commit finishes the transaction and keeps its backup for undo.
func (tx *Tx) Commit(mods []string) error {
	os.RemoveAll(tx.staging)
	if len(tx.rec.Entries) == 0 {
		return os.RemoveAll(tx.dir)
	}
	tx.rec.Mods = mods
	b, err := json.MarshalIndent(tx.rec, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tx.dir, "record.json"), b, 0o644); err != nil {
		return err
	}
	tx.m.prune()
	return nil
}

// Rollback undoes everything this transaction did so far.
func (tx *Tx) Rollback() error {
	err := tx.m.undo(tx.dir, tx.rec)
	os.RemoveAll(tx.staging)
	os.RemoveAll(tx.dir)
	return err
}

// History returns the finished transactions, newest first.
func (m *Manager) History() []Record {
	dirs, err := os.ReadDir(m.Root)
	if err != nil {
		return nil
	}
	var out []Record
	for _, d := range dirs {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.Root, d.Name(), "record.json"))
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

// UndoLast reverts the newest transaction.
func (m *Manager) UndoLast() (*Record, error) {
	h := m.History()
	if len(h) == 0 {
		return nil, ErrNothingToUndo
	}
	r := h[0]
	dir := filepath.Join(m.Root, r.ID)
	if err := m.undo(dir, r); err != nil {
		return nil, err
	}
	return &r, os.RemoveAll(dir)
}

func (m *Manager) undo(dir string, r Record) error {
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for i := len(r.Entries) - 1; i >= 0; i-- {
		e := r.Entries[i]
		if err := m.check(e.Dst); err != nil {
			keep(err)
			continue
		}
		if e.CreatedDir {
			os.Remove(e.Dst) // only succeeds if empty, which is intended
			continue
		}
		if e.Backup == "" {
			keep(os.RemoveAll(e.Dst))
			continue
		}
		// A backed-up path: whatever was placed there later in this
		// transaction has already been removed by the entry after it.
		if _, err := os.Lstat(e.Dst); err == nil {
			keep(os.RemoveAll(e.Dst))
		}
		if err := os.MkdirAll(filepath.Dir(e.Dst), 0o755); err != nil {
			keep(err)
			continue
		}
		keep(move(filepath.Join(dir, filepath.FromSlash(e.Backup)), e.Dst))
	}
	return firstErr
}

// prune deletes old backups beyond Keep.
func (m *Manager) prune() {
	if m.Keep <= 0 {
		return
	}
	h := m.History()
	for i := m.Keep; i < len(h); i++ {
		os.RemoveAll(filepath.Join(m.Root, h[i].ID))
	}
}

// move renames src to dst and falls back to copy+delete across drives.
func move(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if st.IsDir() {
		if err := archive.CopyTree(src, dst); err != nil {
			os.RemoveAll(dst)
			return err
		}
	} else if err := archive.CopyFile(src, dst); err != nil {
		os.Remove(dst)
		return err
	}
	return os.RemoveAll(src)
}

// Move is exported for enabling and disabling mods (moving them between
// the mods folder and a parking folder).
func Move(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("%s already exists", dst)
	}
	return move(src, dst)
}
