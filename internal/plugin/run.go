// Package plugin implements the modes. A plugin cannot skip another step, so
// the decision travels through the workspace: check writes .lane/<step>.key
// and, when that key has already passed, .lane/<step>.skip; the test step's
// first command exits early on the skip file; mark records a pass of the key
// check computed.
package plugin

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/gitremote"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// MarkerPrefix is the marker namespace: a marker is an annotated tag object at
// refs/ci/green/<step>/<key> naming the commit, branch and pipeline that
// passed. Outside refs/tags/, so a plain clone or fetch never downloads them.
const MarkerPrefix = "refs/ci/green/"

// LaneDir holds the per-step decision files, relative to the workspace.
const LaneDir = ".lane"

func MarkerRef(step, key string) string { return MarkerPrefix + step + "/" + key }

func KeyFile(dir, step string) string  { return filepath.Join(dir, LaneDir, step+".key") }
func SkipFile(dir, step string) string { return filepath.Join(dir, LaneDir, step+".skip") }

// Run executes the mode s names, logging to out.
func Run(s settings.Settings, out io.Writer) error {
	if s.Dir == "" {
		s.Dir = "."
	}
	if s.Now == nil {
		s.Now = time.Now
	}
	switch s.Mode {
	case settings.ModeCheck:
		return runCheck(s, out)
	case settings.ModeMark:
		return runMark(s, out)
	case settings.ModePrune:
		return runPrune(s, out)
	case settings.ModeVerify:
		return runVerify(s, out)
	case settings.ModeFiles:
		return runFiles(s, out)
	default:
		return fmt.Errorf("unknown mode %q", s.Mode)
	}
}

func remoteFor(s settings.Settings) gitremote.Remote {
	return gitremote.Remote{URL: s.GitRemote, Token: s.GitHubToken}
}

func logf(out io.Writer, format string, args ...any) {
	fmt.Fprintf(out, "lane-skip: "+format+"\n", args...)
}

// scratchRepo makes a throwaway bare repository to fetch markers into. A
// --depth fetch records the fetched commits as shallow boundaries, so fetching
// into the workspace would cut its history for every later step of the
// pipeline. The scratch repository borrows the workspace's objects (when the
// workspace is a repository) through alternates, so a fetch still transfers
// only what the workspace lacks. A shallow workspace's shallow file comes too:
// without it the scratch repository would claim the borrowed commits' whole
// history, and the server would leave out a marker commit below the boundary.
func scratchRepo(workspace string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "lane-skip-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if _, err := tree.Output("", "init", "-q", "--bare", dir); err != nil {
		cleanup()
		return "", nil, err
	}
	objects, err := tree.Output(workspace, "rev-parse", "--path-format=absolute", "--git-path", "objects")
	if err != nil {
		return dir, cleanup, nil // not a repository: nothing to borrow
	}
	alternates := filepath.Join(dir, "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(strings.TrimSpace(string(objects))+"\n"), 0o644); err != nil {
		cleanup()
		return "", nil, err
	}
	shallowPath, err := tree.Output(workspace, "rev-parse", "--path-format=absolute", "--git-path", "shallow")
	if err != nil {
		cleanup()
		return "", nil, err
	}
	shallow, err := os.ReadFile(strings.TrimSpace(string(shallowPath)))
	switch {
	case err == nil:
		if err := os.WriteFile(filepath.Join(dir, "shallow"), shallow, 0o644); err != nil {
			cleanup()
			return "", nil, err
		}
	case !errors.Is(err, fs.ErrNotExist):
		cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
}
