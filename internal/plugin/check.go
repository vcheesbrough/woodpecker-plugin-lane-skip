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

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/key"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// fetchAttempts bounds the retries of a marker fetch: a --depth fetch takes the
// clone's shallow lock, which another step checking in the same clone may hold.
const fetchAttempts = 5

var fetchBackoff = time.Second

// runCheck decides, per step, whether its key has already passed. Any doubt
// means "run": an error never writes a skip file. It fails only on a workflow
// typo (no steps, or a step no lane declares), which is the same every time.
func runCheck(s settings.Settings, out io.Writer) error {
	if len(s.Steps) == 0 {
		return errors.New("mode check: steps is required")
	}
	for _, step := range s.Steps {
		if !lanes.ValidStepName(step) {
			return fmt.Errorf("mode check: step %q is not a valid step name", step)
		}
	}
	// A decision left by an earlier run in this workspace must never skip.
	for _, step := range s.Steps {
		for _, f := range []string{SkipFile(s.Dir, step), KeyFile(s.Dir, step)} {
			if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("removing the stale %s: %w", f, err)
			}
		}
	}
	runAll := func(format string, args ...any) error {
		why := fmt.Sprintf(format, args...)
		for _, step := range s.Steps {
			logf(out, "WARNING: %s — running %s", why, step)
		}
		return nil
	}

	t, err := tree.Read(s.Dir, "HEAD")
	if err != nil {
		return runAll("could not read HEAD: %v", err)
	}
	cfg, err := key.LoadConfig(t, s.Config)
	if err != nil {
		return runAll("could not read the lane config: %v", err)
	}
	for _, step := range s.Steps {
		if _, ok := cfg.LaneOf(step); !ok {
			return fmt.Errorf("mode check: step %q is not in any lane of %s (declared: %s)", step, cfg.Path, strings.Join(cfg.Steps(), ", "))
		}
	}

	type candidate struct{ step, lane, key, ref string }
	var candidates []candidate
	keys := map[string]string{}
	for _, step := range s.Steps {
		lane, _ := cfg.LaneOf(step)
		k, ok := keys[lane.Name]
		if !ok {
			if k, err = key.Lane(t, cfg, lane); err != nil {
				logf(out, "WARNING: could not compute the %s key: %v — running %s", lane.Name, err, step)
				continue
			}
			keys[lane.Name] = k
		}
		if err := writeFile(KeyFile(s.Dir, step), k+"\n"); err != nil {
			logf(out, "WARNING: could not write %s: %v; mark will not record this run", KeyFile(s.Dir, step), err)
		}
		if s.FullRunSet() {
			logf(out, "FULL_RUN=%s — running %s (lane %s key %s)", s.FullRun, step, lane.Name, k)
			continue
		}
		candidates = append(candidates, candidate{step, lane.Name, k, MarkerRef(step, k)})
	}
	if len(candidates) == 0 {
		return nil
	}

	remote := remoteFor(s)
	var found map[string]bool
	if remote.URL == "" {
		err = errors.New("no remote (set git_remote, or CI_REPO)")
	} else {
		refs := make([]string, len(candidates))
		for i, c := range candidates {
			refs[i] = c.ref
		}
		found, err = remote.Existing(refs...)
	}
	for _, c := range candidates {
		switch {
		case err != nil:
			logf(out, "WARNING: could not read markers: %v — running %s (lane %s key %s)", err, c.step, c.lane, c.key)
		case !found[c.ref]:
			logf(out, "no green marker for %s at lane %s key %s — running", c.step, c.lane, c.key)
		default:
			body := fmt.Sprintf("lane-skip: SKIPPING %s — lane %s key %s has already passed:\n%s"+
				"lane-skip: set FULL_RUN=1 on a manual pipeline to run it anyway\n",
				c.step, c.lane, c.key, indent(markerMessage(s, c.step, c.ref)))
			if werr := writeFile(SkipFile(s.Dir, c.step), body); werr != nil {
				logf(out, "WARNING: could not write %s: %v — running %s", SkipFile(s.Dir, c.step), werr, c.step)
				continue
			}
			fmt.Fprint(out, body)
		}
	}
	return nil
}

// markerMessage fetches a marker into a per-step local ref (both emulator
// steps of a pipeline may be checking at once in one shared clone) and returns
// its message. A marker that exists but cannot be read still skips: ls-remote
// already proved the key passed.
func markerMessage(s settings.Settings, step, ref string) string {
	remote := remoteFor(s)
	local := "refs/lane-skip/check/" + step
	var err error
	for attempt := 1; attempt <= fetchAttempts; attempt++ {
		if err = remote.Fetch(s.Dir, "+"+ref+":"+local); err == nil {
			break
		}
		if attempt < fetchAttempts {
			time.Sleep(fetchBackoff)
		}
	}
	if err != nil {
		return fmt.Sprintf("(%s — marker unreadable: %s)\n", ref, lastLine(err.Error()))
	}
	defer func() { _ = tree.Git(s.Dir, "update-ref", "-d", local).Run() }()
	raw, err := tree.Output(s.Dir, "cat-file", "tag", local)
	if err != nil {
		return fmt.Sprintf("(%s — marker unreadable: %s)\n", ref, lastLine(err.Error()))
	}
	_, msg, _ := strings.Cut(string(raw), "\n\n")
	return msg
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// writeFile writes through a temporary file and a rename, so a reader never
// sees half a file: for a skip file, existing at all means "skip".
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
