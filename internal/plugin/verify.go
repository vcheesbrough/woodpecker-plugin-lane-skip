package plugin

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/key"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// runVerify fails when the lane config does not parse, or names a context or
// input the commit does not have: a typo there silently drops files from a
// key, and a key that covers too little skips tests a change can break.
func runVerify(s settings.Settings, out io.Writer) error {
	t, err := tree.Read(s.Dir, "HEAD")
	if err != nil {
		return err
	}
	cfg, err := key.LoadConfig(t, s.Config)
	if err != nil {
		return err
	}
	var problems []string
	for _, name := range cfg.Names() {
		lane := cfg.Lanes[name]
		if lane.Context != "" {
			if _, ok := t.Lookup(lane.Context); !ok {
				problems = append(problems, fmt.Sprintf("lane %s: context %s is not a file in HEAD", name, lane.Context))
			}
		}
		for _, in := range lane.Inputs {
			if !t.Has(in) {
				problems = append(problems, fmt.Sprintf("lane %s: input %s does not exist in HEAD", name, in))
			}
		}
	}
	if len(problems) > 0 {
		for _, p := range problems {
			logf(out, "%s", p)
		}
		return errors.New(strings.Join(problems, "; "))
	}
	for _, name := range cfg.Names() {
		lane := cfg.Lanes[name]
		covered, err := key.Covered(t, cfg, lane)
		if err != nil {
			return fmt.Errorf("lane %s: %w", name, err)
		}
		logf(out, "lane %s: steps %s, %d files, key %s", name, strings.Join(lane.Steps, ", "), len(covered), key.Key(covered))
	}
	return nil
}

// runFiles prints every file a lane's key covers, one path per line, for a
// repository's own checks of what its lanes cover.
func runFiles(s settings.Settings, out io.Writer) error {
	if s.Lane == "" {
		return errors.New("mode files: lane is required")
	}
	t, err := tree.Read(s.Dir, "HEAD")
	if err != nil {
		return err
	}
	cfg, err := key.LoadConfig(t, s.Config)
	if err != nil {
		return err
	}
	lane, ok := cfg.Lanes[s.Lane]
	if !ok {
		return fmt.Errorf("mode files: lane %q is not in %s (declared: %s)", s.Lane, cfg.Path, strings.Join(cfg.Names(), ", "))
	}
	covered, err := key.Covered(t, cfg, lane)
	if err != nil {
		return err
	}
	for _, e := range covered {
		fmt.Fprintln(out, e.Path)
	}
	return nil
}
