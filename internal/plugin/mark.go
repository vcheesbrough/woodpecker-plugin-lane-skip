package plugin

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/key"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// runMark records that step passed under the key check computed before the
// tests ran. It runs after tests that passed, so once it knows the step it
// never fails: a missing marker only costs a re-run next time, and every
// failure is said out loud instead. Only a workflow typo — no step, or a step
// the lane config does not declare — fails.
func runMark(s settings.Settings, out io.Writer) error {
	step := s.Step
	if step == "" {
		return errors.New("mode mark: step is required")
	}
	if !lanes.ValidStepName(step) {
		return fmt.Errorf("mode mark: step %q is not a valid step name", step)
	}
	warn := func(format string, args ...any) error {
		logf(out, "WARNING: %s; the next pipeline with this key will re-run %s", fmt.Sprintf(format, args...), step)
		return nil
	}

	laneName := "unknown"
	if t, err := tree.Read(s.Dir, "HEAD"); err != nil {
		logf(out, "WARNING: could not read HEAD: %v", err)
	} else if cfg, err := key.LoadConfig(t, s.Config); err != nil {
		logf(out, "WARNING: could not read the lane config: %v", err)
	} else if lane, ok := cfg.LaneOf(step); !ok {
		return fmt.Errorf("mode mark: step %q is not in any lane of %s (declared: %s)", step, cfg.Path, strings.Join(cfg.Steps(), ", "))
	} else {
		laneName = lane.Name
	}

	raw, err := os.ReadFile(KeyFile(s.Dir, step))
	if err != nil {
		return warn("no key from check (%v)", err)
	}
	k := strings.TrimSpace(string(raw))
	if !key.Valid(k) {
		return warn("%s does not hold a key", KeyFile(s.Dir, step))
	}
	remote := remoteFor(s)
	if remote.URL == "" {
		return warn("no remote (set git_remote, or CI_REPO)")
	}
	ref := MarkerRef(step, k)
	if found, err := remote.Existing(ref); err == nil && found[ref] {
		logf(out, "%s at lane %s key %s was already marked green", step, laneName, k)
		return nil
	}

	commit, err := tree.Output(s.Dir, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return warn("could not read HEAD: %v", err)
	}
	message := fmt.Sprintf("%s passed at lane %s key %s\n\ncommit: %s\nbranch: %s\npipeline: %s %s\n",
		step, laneName, k, strings.TrimSpace(string(commit)), orUnknown(s.Branch), orUnknown(s.PipelineNum), s.PipelineURL)
	obj, err := remote.MakeTag(s.Dir, strings.TrimSpace(string(commit)), "ci/green/"+step+"/"+k, message, s.Now())
	if err != nil {
		return warn("could not create the marker tag object: %v", err)
	}
	pushErr := remote.CreateRef(s.Dir, obj, ref)
	if pushErr == nil {
		logf(out, "marked %s green at lane %s key %s", step, laneName, k)
		return nil
	}
	// Create-only: another pipeline with this key may have marked it first.
	if found, err := remote.Existing(ref); err == nil && found[ref] {
		logf(out, "%s at lane %s key %s was already marked green", step, laneName, k)
		return nil
	}
	return warn("could not record the green marker %s: %v", ref, pushErr)
}

func orUnknown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "unknown"
	}
	return v
}
