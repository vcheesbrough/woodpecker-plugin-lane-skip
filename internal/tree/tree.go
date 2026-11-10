// Package tree reads a commit's tree from a local repository. Everything a
// lane key covers comes from here, so a key is the commit's content and never
// the working tree.
package tree

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Entry is one file of a tree: `git ls-tree -r` without the type column.
type Entry struct {
	Mode   string
	Object string
	Path   string
}

// Tree is every file of one commit, in `git ls-tree` order.
type Tree struct {
	dir     string
	Entries []Entry
	byPath  map[string]Entry
}

// Git runs git in dir with the workspace treated as safe: CI mounts the
// checkout into a container under another user.
func Git(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*"}, args...)...)
	if dir != "" {
		cmd.Dir = dir
		if abs, err := filepath.Abs(dir); err == nil {
			cmd.Dir = abs
		}
	}
	return cmd
}

// Output runs git and returns its stdout, folding stderr into the error.
func Output(dir string, args ...string) ([]byte, error) {
	cmd := Git(dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return out, nil
}

// Read lists every file of rev (normally HEAD) in the repository at dir.
func Read(dir, rev string) (*Tree, error) {
	out, err := Output(dir, "ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, err
	}
	t := &Tree{dir: dir, byPath: map[string]Entry{}}
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := bytes.Cut(rec, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("unexpected ls-tree line %q", rec)
		}
		e := Entry{Mode: fields[0], Object: fields[2], Path: string(path)}
		t.Entries = append(t.Entries, e)
		t.byPath[e.Path] = e
	}
	return t, nil
}

// Lookup returns the file at path, if the tree has one.
func (t *Tree) Lookup(path string) (Entry, bool) {
	e, ok := t.byPath[path]
	return e, ok
}

// Has reports whether path is a file of the tree or a directory holding one.
func (t *Tree) Has(path string) bool {
	if _, ok := t.byPath[path]; ok {
		return true
	}
	prefix := path + "/"
	for _, e := range t.Entries {
		if strings.HasPrefix(e.Path, prefix) {
			return true
		}
	}
	return false
}

// ErrNotFound is returned by ReadFile for a path the tree does not hold.
var ErrNotFound = errors.New("not in the commit")

// ReadFile returns the content of a file of the tree.
func (t *Tree) ReadFile(path string) ([]byte, error) {
	e, ok := t.byPath[path]
	if !ok {
		return nil, fmt.Errorf("%s: %w", path, ErrNotFound)
	}
	return Output(t.dir, "cat-file", "blob", e.Object)
}
