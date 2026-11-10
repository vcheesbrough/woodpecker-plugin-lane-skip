package settings

import (
	"reflect"
	"strings"
	"testing"
)

func clearEnv(t *testing.T) {
	for _, k := range []string{"GITHUB_ACTIONS", "GITHUB_TOKEN", "GITHUB_REPOSITORY", "GITHUB_REF_NAME", "GITHUB_HEAD_REF",
		"GITHUB_RUN_NUMBER", "GITHUB_RUN_ID", "GITHUB_SERVER_URL", "CI_REPO", "CI_COMMIT_BRANCH", "CI_PIPELINE_NUMBER",
		"CI_PIPELINE_URL", "FULL_RUN"} {
		t.Setenv(k, "")
	}
}

func TestWoodpeckerSettings(t *testing.T) {
	clearEnv(t)
	t.Setenv("PLUGIN_MODE", "Check")
	t.Setenv("PLUGIN_STEPS", "android-api-29,android-api-36")
	t.Setenv("PLUGIN_GITHUB_TOKEN", "tok")
	t.Setenv("CI_REPO", "owner/repo")
	t.Setenv("CI_COMMIT_BRANCH", "feat/x")
	t.Setenv("CI_PIPELINE_NUMBER", "7")
	t.Setenv("CI_PIPELINE_URL", "https://ci/7")
	t.Setenv("GITHUB_REF_NAME", "ignored-outside-actions")
	s, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeCheck || !reflect.DeepEqual(s.Steps, []string{"android-api-29", "android-api-36"}) ||
		s.GitHubToken != "tok" || s.GitRemote != "https://github.com/owner/repo.git" || s.Config != ".woodpecker/lanes.yml" ||
		s.Branch != "feat/x" || s.PipelineNum != "7" || s.PipelineURL != "https://ci/7" || s.FullRunSet() {
		t.Fatalf("settings = %+v", s)
	}
}

func TestGitHubActionsInputs(t *testing.T) {
	clearEnv(t)
	t.Setenv("PLUGIN_MODE", "")
	t.Setenv("INPUT_MODE", "mark")
	t.Setenv("INPUT_STEP", "e2e")
	t.Setenv("INPUT_CONFIG", "ci/lanes.yml")
	t.Setenv("INPUT_GIT_REMOTE", "file:///tmp/r.git")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_TOKEN", "envtok")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_REF_NAME", "12/merge")
	t.Setenv("GITHUB_HEAD_REF", "feat/y")
	t.Setenv("GITHUB_RUN_NUMBER", "42")
	t.Setenv("GITHUB_RUN_ID", "999")
	s, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeMark || s.Step != "e2e" || s.Config != "ci/lanes.yml" || s.GitRemote != "file:///tmp/r.git" ||
		s.GitHubToken != "envtok" || s.Branch != "feat/y" || s.PipelineNum != "42" ||
		s.PipelineURL != "https://github.com/owner/repo/actions/runs/999" {
		t.Fatalf("settings = %+v", s)
	}
}

func TestStepsAsJSON(t *testing.T) {
	clearEnv(t)
	t.Setenv("PLUGIN_MODE", "check")
	t.Setenv("PLUGIN_STEPS", `["a","b"]`)
	s, err := FromEnv()
	if err != nil || !reflect.DeepEqual(s.Steps, []string{"a", "b"}) {
		t.Fatalf("steps = %v, %v", s.Steps, err)
	}
}

func TestFullRun(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "false": false, "FALSE": false, "no": false, "off": false,
		"1": true, "true": true, "yes": true, "anything": true} {
		if got := (Settings{FullRun: raw}).FullRunSet(); got != want {
			t.Errorf("FullRunSet(%q) = %v, want %v", raw, got, want)
		}
	}
	clearEnv(t)
	t.Setenv("PLUGIN_MODE", "check")
	t.Setenv("PLUGIN_FULL_RUN", "")
	t.Setenv("FULL_RUN", "1")
	if s, _ := FromEnv(); !s.FullRunSet() {
		t.Fatal("FULL_RUN in the environment does not force a run")
	}
	t.Setenv("PLUGIN_FULL_RUN", "true")
	t.Setenv("FULL_RUN", "")
	if s, _ := FromEnv(); !s.FullRunSet() {
		t.Fatal("the full_run setting does not force a run")
	}
}

func TestModeIsRequiredAndKnown(t *testing.T) {
	clearEnv(t)
	t.Setenv("PLUGIN_MODE", "")
	t.Setenv("INPUT_MODE", "")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), "mode is required") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("PLUGIN_MODE", "skip")
	if _, err := FromEnv(); err == nil || !strings.Contains(err.Error(), `unknown mode "skip"`) {
		t.Fatalf("err = %v", err)
	}
}
