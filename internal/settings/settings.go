// Package settings reads the plugin's settings: Woodpecker hands them over as
// PLUGIN_* variables, GitHub Actions as INPUT_*; CI metadata comes from
// Woodpecker's CI_* variables, or GitHub's own when GITHUB_ACTIONS=true.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
)

type Mode string

const (
	ModeCheck  Mode = "check"
	ModeMark   Mode = "mark"
	ModePrune  Mode = "prune"
	ModeVerify Mode = "verify"
	ModeFiles  Mode = "files"
)

type Settings struct {
	Mode    Mode
	Dir     string // the workspace checkout; .lane/ is written here
	Config  string // lane config path inside the commit
	Steps   []string
	Step    string
	Lane    string
	Days    string
	DryRun  bool
	FullRun string // the raw value, for the log line; see FullRunSet

	GitRemote   string
	GitHubToken string

	Branch      string
	PipelineNum string
	PipelineURL string

	// Now dates a marker; tests set it to make old markers.
	Now func() time.Time
}

// FromEnv reads the settings from the environment.
func FromEnv() (Settings, error) {
	gha := strings.EqualFold(os.Getenv("GITHUB_ACTIONS"), "true")
	ci := func(woodpecker string, github ...string) string {
		if v := env(woodpecker); v != "" {
			return v
		}
		if gha {
			return env(github...)
		}
		return ""
	}
	s := Settings{
		Mode:        Mode(strings.ToLower(setting("MODE"))),
		Dir:         ".",
		Config:      firstNonEmpty(setting("CONFIG"), lanes.DefaultPath),
		Steps:       splitList(setting("STEPS")),
		Step:        setting("STEP"),
		Lane:        setting("LANE"),
		Days:        setting("DAYS"),
		DryRun:      isTrue(setting("DRY_RUN")),
		FullRun:     firstNonEmpty(setting("FULL_RUN"), env("FULL_RUN")),
		GitRemote:   setting("GIT_REMOTE"),
		GitHubToken: firstNonEmpty(setting("GITHUB_TOKEN"), env("GITHUB_TOKEN")),
		Branch:      ci("CI_COMMIT_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME"),
		PipelineNum: ci("CI_PIPELINE_NUMBER", "GITHUB_RUN_NUMBER"),
		PipelineURL: env("CI_PIPELINE_URL"),
		Now:         time.Now,
	}
	if s.PipelineURL == "" && gha && env("GITHUB_RUN_ID") != "" {
		s.PipelineURL = fmt.Sprintf("%s/%s/actions/runs/%s",
			firstNonEmpty(env("GITHUB_SERVER_URL"), "https://github.com"), env("GITHUB_REPOSITORY"), env("GITHUB_RUN_ID"))
	}
	if s.GitRemote == "" {
		if repo := ci("CI_REPO", "GITHUB_REPOSITORY"); repo != "" {
			s.GitRemote = "https://github.com/" + repo + ".git"
		}
	}
	switch s.Mode {
	case ModeCheck, ModeMark, ModePrune, ModeVerify, ModeFiles:
	case "":
		return s, fmt.Errorf("mode is required: check, mark, prune, verify or files")
	default:
		return s, fmt.Errorf("unknown mode %q: expected check, mark, prune, verify or files", s.Mode)
	}
	return s, nil
}

// FullRunSet reports whether full_run / FULL_RUN asks for every step to run:
// anything but "", 0, false, no or off.
func (s Settings) FullRunSet() bool {
	switch strings.ToLower(strings.TrimSpace(s.FullRun)) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

// splitList accepts Woodpecker's comma-joined list or a JSON array.
func splitList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if strings.HasPrefix(raw, "[") && json.Unmarshal([]byte(raw), &out) == nil {
		return out
	}
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func setting(name string) string {
	return env("PLUGIN_"+name, "INPUT_"+name)
}

func env(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func isTrue(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
