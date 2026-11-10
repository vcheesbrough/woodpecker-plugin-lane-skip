// Package plugin implements the modes. A plugin cannot skip another step, so
// the decision travels through the workspace: check writes .lane/<step>.key
// and, when that key has already passed, .lane/<step>.skip; the test step's
// first command exits early on the skip file; mark records a pass of the key
// check computed.
package plugin

import (
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/gitremote"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
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
