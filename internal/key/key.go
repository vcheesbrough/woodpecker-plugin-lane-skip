// Package key computes a lane's key: a sha256 over the files the lane covers,
// read from the commit's tree.
package key

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// Header is the first line hashed into every key. Bump its version whenever
// what goes into a key changes, so no old marker can match a key computed
// under different rules. TestGoldenKey pins it.
const Header = "lane-skip key v1\n"

// Key hashes the header and one `<mode> <object> <path>` line per file, in
// byte order of path, so the same files give the same key on any machine.
func Key(entries []tree.Entry) string {
	sorted := append([]tree.Entry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })
	h := sha256.New()
	h.Write([]byte(Header))
	for _, e := range sorted {
		fmt.Fprintf(h, "%s %s %s\n", e.Mode, e.Object, e.Path)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// IgnoreFile names the ignore file BuildKit applies to a build of dockerfile
// with the repository root as its context: `<dockerfile>.dockerignore` beside
// it if the commit has one, else the root `.dockerignore`, else none ("").
func IgnoreFile(t *tree.Tree, dockerfile string) string {
	for _, candidate := range []string{dockerfile + ".dockerignore", ".dockerignore"} {
		if _, ok := t.Lookup(candidate); ok {
			return candidate
		}
	}
	return ""
}

// Context returns the files of the tree that a build of dockerfile, with the
// repository root as its context, sends to the builder. The ignore file is the
// commit's too, so an uncommitted edit cannot change a key. Matching is
// Docker's own (moby/patternmatcher): anchored at the root, `*` and `?` stop
// at `/`, `**` does not, a match on a directory excludes what is under it, `!`
// re-admits, and the last matching pattern wins.
func Context(t *tree.Tree, dockerfile string) ([]tree.Entry, error) {
	if _, ok := t.Lookup(dockerfile); !ok {
		return nil, fmt.Errorf("context %s: %w", dockerfile, tree.ErrNotFound)
	}
	ignore := IgnoreFile(t, dockerfile)
	if ignore == "" {
		return append([]tree.Entry(nil), t.Entries...), nil
	}
	data, err := t.ReadFile(ignore)
	if err != nil {
		return nil, err
	}
	patterns, err := ignorefile.ReadAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ignore, err)
	}
	pm, err := patternmatcher.New(patterns)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ignore, err)
	}
	var out []tree.Entry
	for _, e := range t.Entries {
		excluded, _, err := pm.MatchesUsingParentResults(e.Path, patternmatcher.MatchInfo{})
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ignore, err)
		}
		if !excluded {
			out = append(out, e)
		}
	}
	return out, nil
}

// Covered returns every file a lane's key covers: its build context (when it
// has one), its declared inputs, and implicitly the lane config, the Dockerfile
// and the ignore file in use. Each file appears once, in path order.
func Covered(t *tree.Tree, cfg *lanes.Config, lane *lanes.Lane) ([]tree.Entry, error) {
	seen := map[string]tree.Entry{}
	add := func(e tree.Entry) { seen[e.Path] = e }

	paths := append([]string{cfg.Path}, lane.Inputs...)
	if lane.Context != "" {
		ctx, err := Context(t, lane.Context)
		if err != nil {
			return nil, err
		}
		for _, e := range ctx {
			add(e)
		}
		paths = append(paths, lane.Context)
		if ignore := IgnoreFile(t, lane.Context); ignore != "" {
			paths = append(paths, ignore)
		}
	}
	for _, e := range t.Entries {
		for _, p := range paths {
			if e.Path == p || strings.HasPrefix(e.Path, p+"/") {
				add(e)
				break
			}
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("lane " + lane.Name + " covers no files")
	}
	out := make([]tree.Entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Lane computes one lane's key.
func Lane(t *tree.Tree, cfg *lanes.Config, lane *lanes.Lane) (string, error) {
	covered, err := Covered(t, cfg, lane)
	if err != nil {
		return "", err
	}
	return Key(covered), nil
}

// LoadConfig reads and parses the lane config from the tree.
func LoadConfig(t *tree.Tree, configPath string) (*lanes.Config, error) {
	data, err := t.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	return lanes.Parse(configPath, data)
}

// Valid reports whether s has the shape of a key.
func Valid(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}
