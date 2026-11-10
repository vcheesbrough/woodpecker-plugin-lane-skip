package plugin

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

const (
	pruneNS     = "refs/lane-skip/prune/"
	deleteChunk = 100
)

// runPrune deletes every marker older than days. Deleting a marker is always
// safe: it only costs that step one re-run. The tag dates are read by fetching
// the markers into a private local namespace (shallow: only the tag objects
// and their commits), which is removed again afterwards.
func runPrune(s settings.Settings, out io.Writer) error {
	days, err := strconv.Atoi(strings.TrimSpace(s.Days))
	if err != nil || days < 0 {
		return fmt.Errorf("mode prune: days must be a whole number of days, got %q", s.Days)
	}
	remote := remoteFor(s)
	if remote.URL == "" {
		return fmt.Errorf("mode prune: no remote (set git_remote, or CI_REPO)")
	}
	cutoff := s.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()

	if err := clearNamespace(s.Dir); err != nil {
		return err
	}
	defer func() {
		if err := clearNamespace(s.Dir); err != nil {
			logf(out, "WARNING: %v", err)
		}
	}()
	if err := remote.Fetch(s.Dir, "+"+MarkerPrefix+"*:"+pruneNS+"*"); err != nil {
		return fmt.Errorf("could not fetch the markers: %w", err)
	}
	listing, err := tree.Output(s.Dir, "for-each-ref", "--format=%(taggerdate:unix) %(refname)", pruneNS)
	if err != nil {
		return err
	}
	var old []string
	total := 0
	for _, line := range strings.Split(strings.TrimSpace(string(listing)), "\n") {
		date, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		total++
		when, err := strconv.ParseInt(date, 10, 64)
		if err != nil || when >= cutoff {
			continue // no tagger date is not old: only our markers are dated
		}
		old = append(old, MarkerPrefix+strings.TrimPrefix(ref, pruneNS))
	}
	logf(out, "%d of %d markers are older than %d days", len(old), total, days)
	if len(old) == 0 {
		return nil
	}
	if s.DryRun {
		for _, ref := range old {
			fmt.Fprintf(out, "  would delete %s\n", ref)
		}
		return nil
	}
	for i := 0; i < len(old); i += deleteChunk {
		end := min(i+deleteChunk, len(old))
		if err := remote.Delete(s.Dir, old[i:end]...); err != nil {
			return fmt.Errorf("deleting markers failed after %d of %d: %w", i, len(old), err)
		}
	}
	logf(out, "deleted %d markers", len(old))
	return nil
}

func clearNamespace(dir string) error {
	refs, err := tree.Output(dir, "for-each-ref", "--format=delete %(refname)", pruneNS)
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(refs))) == 0 {
		return nil
	}
	cmd := tree.Git(dir, "update-ref", "--stdin")
	cmd.Stdin = strings.NewReader(string(refs))
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("could not remove %s*: %s: %w", pruneNS, strings.TrimSpace(string(b)), err)
	}
	return nil
}
