package plugin_test

// Ports v-note's scripts/test-lane-key.sh: which change moves which key, the
// check/mark round trip per step (FULL_RUN, an existing marker, concurrent
// steps in one shared clone, every failure path), prune and verify. Real git
// against a scratch repository and a local bare remote.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/key"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/plugin"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/testgit"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

const lanesYML = `lanes:
  android:
    context: Dockerfile.android
    inputs: [.woodpecker/android.yml, scripts/android-build-box-image.ref, scripts/check-image-metadata.sh]
    steps: [android-api-29, android-api-36]
  web:
    context: Dockerfile.web
    inputs: [.woodpecker/web.yml, e2e, deploy/grafana, scripts/check-image-metadata.sh]
    steps: [e2e-web]
  unit:
    inputs: [crates, Cargo.toml, Cargo.lock]
    steps: [cargo-test]
`

// A repository shaped like v-note's: an android and a web build context, and
// a lane with no context at all.
func fixture(t *testing.T) string {
	t.Helper()
	return testgit.Repo(t, map[string]string{
		".woodpecker/lanes.yml":               lanesYML,
		".woodpecker/android.yml":             "steps: {}\n",
		".woodpecker/web.yml":                 "steps: {}\n",
		".woodpecker/checks.yml":              "steps: {}\n",
		"Dockerfile.android":                  "FROM scratch\n",
		"Dockerfile.android.dockerignore":     "*\n!android\n!contracts\n!Cargo.toml\n!Cargo.lock\n!scripts/sync-version.sh\n",
		"Dockerfile.web":                      "FROM scratch\n",
		"Dockerfile.web.dockerignore":         "# the web image\nandroid\ndocs\ne2e\ndeploy\n*.md\n.woodpecker\nscripts\ncrates/*/tests\ncontracts\nDockerfile.android*\n",
		"AGENTS.md":                           "agents\n",
		"README.md":                           "readme\n",
		"docs/guide.md":                       "guide\n",
		"android/app/build.gradle":            "gradle\n",
		"android/app/src/Main.kt":             "kotlin\n",
		"contracts/api.json":                  "{}\n",
		"crates/server/src/main.rs":           "fn main() {}\n",
		"crates/server/tests/it.rs":           "#[test] fn t() {}\n",
		"frontend/index.html":                 "<html>\n",
		"e2e/tests/a.spec.ts":                 "test\n",
		"deploy/grafana/dashboard.json":       "{}\n",
		"deploy/docker-compose.yml":           "services: {}\n",
		"Cargo.toml":                          "[workspace]\n",
		"Cargo.lock":                          "# lock\n",
		"scripts/android-build-box-image.ref": "image@sha256:0\n",
		"scripts/check-image-metadata.sh":     "#!/bin/sh\n",
		"scripts/sync-version.sh":             "#!/bin/sh\n",
		"scripts/release-version.sh":          "#!/bin/sh\n",
	})
}

func laneKeys(t *testing.T, dir string) map[string]string {
	t.Helper()
	tr, err := tree.Read(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := key.LoadConfig(tr, lanes.DefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, name := range cfg.Names() {
		if out[name], err = key.Lane(tr, cfg, cfg.Lanes[name]); err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestWhichChangeMovesWhichKey(t *testing.T) {
	dir := fixture(t)
	a, b := laneKeys(t, dir), laneKeys(t, dir)
	if a["android"] != b["android"] || a["web"] != b["web"] || a["unit"] != b["unit"] {
		t.Fatal("keys differ between two runs")
	}
	if a["android"] == a["web"] || a["web"] == a["unit"] {
		t.Fatal("lanes share a key")
	}

	appendTo := func(p string) func() { return func() { testgit.Append(t, dir, p) } }
	touch := func(p string) func() { return func() { testgit.Write(t, dir, map[string]string{p: ""}) } }
	chmodX := func(p string) func() {
		return func() {
			if err := os.Chmod(filepath.Join(dir, p), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	const same, changed = "same", "changed"
	for _, tc := range []struct {
		desc               string
		android, web, unit string
		change             func()
	}{
		{"empty commit", same, same, same, func() {}},
		{"docs/ change", same, same, same, appendTo("docs/guide.md")},
		{"AGENTS.md change", same, same, same, appendTo("AGENTS.md")},
		{"README.md change", same, same, same, appendTo("README.md")},
		{".woodpecker/checks.yml change", same, same, same, appendTo(".woodpecker/checks.yml")},
		{"release-version.sh change", same, same, same, appendTo("scripts/release-version.sh")},
		{"deploy/docker-compose.yml change", same, same, same, appendTo("deploy/docker-compose.yml")},
		{"android/ change", changed, same, same, appendTo("android/app/build.gradle")},
		{"android/ file added", changed, same, same, touch("android/new-file")},
		{"android/ mode change", changed, same, same, chmodX("android/app/src/Main.kt")},
		{"contracts/ change", changed, same, same, appendTo("contracts/api.json")},
		{"sync-version.sh change", changed, same, same, appendTo("scripts/sync-version.sh")},
		{"build-box pin change", changed, same, same, appendTo("scripts/android-build-box-image.ref")},
		{".woodpecker/android.yml change", changed, same, same, appendTo(".woodpecker/android.yml")},
		{"Dockerfile.android change", changed, same, same, appendTo("Dockerfile.android")},
		{"android .dockerignore change", changed, same, same, appendTo("Dockerfile.android.dockerignore")},
		{"e2e/ change", same, changed, same, appendTo("e2e/tests/a.spec.ts")},
		{"frontend/ change", same, changed, same, appendTo("frontend/index.html")},
		{"deploy/grafana change", same, changed, same, appendTo("deploy/grafana/dashboard.json")},
		{".woodpecker/web.yml change", same, changed, same, appendTo(".woodpecker/web.yml")},
		{"Dockerfile.web change", same, changed, same, appendTo("Dockerfile.web")},
		{"web .dockerignore change", same, changed, same, appendTo("Dockerfile.web.dockerignore")},
		{"server integration test change", same, same, changed, appendTo("crates/server/tests/it.rs")},
		{"crates/ source change", same, changed, changed, appendTo("crates/server/src/main.rs")},
		{"Cargo.toml change", changed, changed, changed, appendTo("Cargo.toml")},
		{"check-image-metadata.sh change", changed, changed, same, appendTo("scripts/check-image-metadata.sh")},
		{"lanes.yml change", changed, changed, changed, appendTo(".woodpecker/lanes.yml")},
		{"new root file", same, changed, same, touch("new-root-file")},
	} {
		before := laneKeys(t, dir)
		tc.change()
		testgit.Commit(t, dir, tc.desc)
		after := laneKeys(t, dir)
		got := map[string]string{}
		for lane := range before {
			got[lane] = same
			if before[lane] != after[lane] {
				got[lane] = changed
			}
		}
		if got["android"] != tc.android || got["web"] != tc.web || got["unit"] != tc.unit {
			t.Errorf("%s: android %s (want %s), web %s (want %s), unit %s (want %s)",
				tc.desc, got["android"], tc.android, got["web"], tc.web, got["unit"], tc.unit)
		}
	}

	// Uncommitted edits are not the commit's content.
	before := laneKeys(t, dir)
	testgit.Append(t, dir, "frontend/index.html")
	testgit.Append(t, dir, "Dockerfile.web.dockerignore")
	testgit.Write(t, dir, map[string]string{"frontend/untracked.js": ""})
	if after := laneKeys(t, dir); after["web"] != before["web"] {
		t.Error("an uncommitted edit moved the web key")
	}
}

type ws struct {
	t      *testing.T
	dir    string
	remote string
}

func newWS(t *testing.T) *ws {
	t.Cleanup(plugin.SetFetchBackoff(10 * time.Millisecond))
	return &ws{t: t, dir: fixture(t), remote: testgit.Bare(t)}
}

func (w *ws) settings(mode settings.Mode) settings.Settings {
	return settings.Settings{Mode: mode, Dir: w.dir, Config: lanes.DefaultPath, GitRemote: w.remote, Now: time.Now}
}

func run(s settings.Settings) (string, error) {
	var b bytes.Buffer
	err := plugin.Run(s, &b)
	return b.String(), err
}

func (w *ws) check(steps ...string) string {
	w.t.Helper()
	s := w.settings(settings.ModeCheck)
	s.Steps = steps
	out, err := run(s)
	if err != nil {
		w.t.Fatalf("check %v: %v\n%s", steps, err, out)
	}
	return out
}

func (w *ws) mark(step string, edit ...func(*settings.Settings)) string {
	w.t.Helper()
	s := w.settings(settings.ModeMark)
	s.Step = step
	for _, e := range edit {
		e(&s)
	}
	out, err := run(s)
	if err != nil {
		w.t.Fatalf("mark %s: %v\n%s", step, err, out)
	}
	return out
}

// fullClone fails the test if the workspace lost history: a marker fetch must
// never make the consumer's clone shallow.
func (w *ws) fullClone(commits string) {
	w.t.Helper()
	if got := testgit.Git(w.t, w.dir, "rev-parse", "--is-shallow-repository"); got != "false" {
		w.t.Fatalf("the workspace became a shallow clone")
	}
	if got := testgit.Git(w.t, w.dir, "rev-list", "--count", "HEAD"); got != commits {
		w.t.Fatalf("HEAD has %s commits, want %s: the workspace lost history", got, commits)
	}
}

// noScratchLeft points TMPDIR at an empty directory and checks, at the end of
// the test, that every scratch repository was removed.
func noScratchLeft(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Cleanup(func() {
		if left, _ := os.ReadDir(tmp); len(left) > 0 {
			t.Errorf("left in TMPDIR: %v", left)
		}
	})
}

func (w *ws) skipping(step string) bool {
	_, err := os.Stat(plugin.SkipFile(w.dir, step))
	return err == nil
}

func (w *ws) keyFile(step string) string {
	w.t.Helper()
	b, err := os.ReadFile(plugin.KeyFile(w.dir, step))
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func (w *ws) markerSHA(ref string) string {
	out, _ := testgit.Try(w.dir, "ls-remote", w.remote, ref)
	sha, _, _ := strings.Cut(strings.TrimSpace(out), "\t")
	return sha
}

func (w *ws) markerMessage(ref string) string {
	w.t.Helper()
	testgit.Git(w.t, w.dir, "fetch", "-q", "--no-tags", w.remote, "+"+ref+":refs/test/marker")
	return testgit.Git(w.t, w.dir, "cat-file", "-p", "refs/test/marker")
}

// The skip guard README tells consumers to put first in the test step.
func guard(t *testing.T, dir, step string) (ran bool, out string) {
	t.Helper()
	cmd := exec.Command("sh", "-c", `[ ! -e .lane/`+step+`.skip ] || { cat .lane/`+step+`.skip; exit 0; }; echo TESTS-RAN; exit 3`)
	cmd.Dir = dir
	b, err := cmd.CombinedOutput()
	if code := cmd.ProcessState.ExitCode(); code != 0 && code != 3 {
		t.Fatalf("guard: %v\n%s", err, b)
	}
	return strings.Contains(string(b), "TESTS-RAN"), string(b)
}

func TestCheckAndMark(t *testing.T) {
	w := newWS(t)
	noScratchLeft(t)
	// History under the marked commit, so a shallow fetch would cut it.
	testgit.Commit(t, w.dir, "history")
	keys := laneKeys(t, w.dir)

	out := w.check("android-api-29")
	if w.skipping("android-api-29") || !strings.Contains(out, "no green marker for android-api-29") {
		t.Fatalf("no marker: want a run\n%s", out)
	}
	if got := w.keyFile("android-api-29"); got != keys["android"] {
		t.Fatalf(".lane key = %s, want the android key %s", got, keys["android"])
	}
	if ran, _ := guard(t, w.dir, "android-api-29"); !ran {
		t.Fatal("the guard skipped without a skip file")
	}

	out = w.mark("android-api-29", func(s *settings.Settings) {
		s.PipelineNum, s.Branch, s.PipelineURL = "7", "feat/x", "https://ci.example/7"
	})
	ref := plugin.MarkerRef("android-api-29", keys["android"])
	if !strings.Contains(out, "marked android-api-29 green") || w.markerSHA(ref) == "" {
		t.Fatalf("mark after a pass: no marker at %s\n%s", ref, out)
	}
	if !strings.HasPrefix(ref, "refs/ci/green/android-api-29/") {
		t.Fatalf("marker ref %s is outside the namespace", ref)
	}
	msg := w.markerMessage(ref)
	head := testgit.Git(t, w.dir, "rev-parse", "HEAD")
	for _, want := range []string{"android-api-29 passed at lane android key " + keys["android"], "commit: " + head,
		"branch: feat/x", "pipeline: 7 https://ci.example/7"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("marker message lacks %q:\n%s", want, msg)
		}
	}

	out = w.check("android-api-29", "android-api-36", "e2e-web")
	if !w.skipping("android-api-29") {
		t.Fatalf("marker: want a skip\n%s", out)
	}
	ran, gout := guard(t, w.dir, "android-api-29")
	if ran || !strings.Contains(gout, "SKIPPING android-api-29") || !strings.Contains(gout, "  pipeline: 7") || !strings.Contains(gout, "  branch: feat/x") {
		t.Fatalf("guard on a skip: ran=%v\n%s", ran, gout)
	}
	if w.skipping("android-api-36") || w.skipping("e2e-web") {
		t.Fatal("a marker is per step: android-api-36 and e2e-web must still run")
	}
	if w.keyFile("android-api-36") != keys["android"] || w.keyFile("e2e-web") != keys["web"] {
		t.Fatal("check wrote the wrong keys")
	}

	// FULL_RUN forces a run, but still leaves the key for mark.
	s := w.settings(settings.ModeCheck)
	s.Steps, s.FullRun = []string{"android-api-29"}, "1"
	if out, err := run(s); err != nil || w.skipping("android-api-29") || !strings.Contains(out, "FULL_RUN=1") {
		t.Fatalf("FULL_RUN=1 must run despite a marker: %v\n%s", err, out)
	}
	if w.keyFile("android-api-29") != keys["android"] {
		t.Fatal("FULL_RUN left no key")
	}
	s.FullRun = "0"
	if _, err := run(s); err != nil || !w.skipping("android-api-29") {
		t.Fatal("FULL_RUN=0 must still skip")
	}

	// Another lane's change keeps the skip; re-marking never overwrites.
	marker := w.markerSHA(ref)
	testgit.Append(t, w.dir, "e2e/tests/a.spec.ts")
	testgit.Commit(t, w.dir, "e2e change")
	w.check("android-api-29")
	if !w.skipping("android-api-29") {
		t.Fatal("another lane's change lost the skip")
	}
	w.fullClone("3") // the marker's commit is HEAD~1
	out = w.mark("android-api-29", func(s *settings.Settings) { s.PipelineNum = "8" })
	if !strings.Contains(out, "already marked") {
		t.Fatalf("mark after a skip: %s", out)
	}
	if w.markerSHA(ref) != marker {
		t.Fatal("an existing marker was overwritten")
	}

	// The lane's own change: run.
	testgit.Append(t, w.dir, "android/app/build.gradle")
	testgit.Commit(t, w.dir, "android change")
	w.check("android-api-29")
	if w.skipping("android-api-29") {
		t.Fatal("own lane's change still skips")
	}

	// A lane without a context.
	w.check("cargo-test")
	w.mark("cargo-test")
	w.check("cargo-test")
	if !w.skipping("cargo-test") {
		t.Fatal("a lane without a context did not skip after a mark")
	}

	if tags := testgit.Git(t, w.dir, "for-each-ref", "refs/tags/", "refs/lane-skip/"); tags != "" {
		t.Fatalf("check and mark left local refs:\n%s", tags)
	}
}

func TestCheckRemovesAStaleSkip(t *testing.T) {
	w := newWS(t)
	testgit.Write(t, w.dir, map[string]string{".lane/e2e-web.skip": "stale\n", ".lane/e2e-web.key": "stale\n"})
	w.check("e2e-web")
	if w.skipping("e2e-web") {
		t.Fatal("a skip file from an earlier run survived check")
	}
}

// The two emulator steps check and mark at the same moment in one shared
// clone; each must land, and print, under its own name.
func TestConcurrentStepsInOneClone(t *testing.T) {
	w := newWS(t)
	steps := []string{"android-api-29", "android-api-36"}
	w.check(steps...)

	var wg sync.WaitGroup
	outs := make([]string, len(steps))
	for i, step := range steps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := w.settings(settings.ModeMark)
			s.Step = step
			outs[i], _ = run(s)
		}()
	}
	wg.Wait()
	k := laneKeys(t, w.dir)["android"]
	for i, step := range steps {
		ref := plugin.MarkerRef(step, k)
		if msg := w.markerMessage(ref); !strings.Contains(msg, "\n"+step+" passed") {
			t.Errorf("concurrent mark: %s missing or mislabelled:\n%s\n%s", step, msg, outs[i])
		}
	}

	for i, step := range steps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := w.settings(settings.ModeCheck)
			s.Steps = []string{step}
			outs[i], _ = run(s)
		}()
	}
	wg.Wait()
	for i, step := range steps {
		b, err := os.ReadFile(plugin.SkipFile(w.dir, step))
		if err != nil || !strings.Contains(string(b), "SKIPPING "+step) || !strings.Contains(string(b), "\n  "+step+" passed") {
			t.Errorf("concurrent check: %s did not print its own marker: %v\n%s\n%s", step, err, b, outs[i])
		}
	}
}

func TestFailuresRunAndNeverFailTheStep(t *testing.T) {
	w := newWS(t)
	w.check("e2e-web")
	w.mark("e2e-web")

	unreachable := func(s *settings.Settings) { s.GitRemote = "file://" + filepath.Join(t.TempDir(), "no-such-remote.git") }
	s := w.settings(settings.ModeCheck)
	s.Steps = []string{"e2e-web"}
	unreachable(&s)
	out, err := run(s)
	if err != nil || w.skipping("e2e-web") || !strings.Contains(out, "WARNING: could not read markers") {
		t.Fatalf("unreachable remote: want a run with a warning: %v\n%s", err, out)
	}
	if out := w.mark("e2e-web", unreachable); !strings.Contains(out, "WARNING") {
		t.Fatalf("unreachable remote: mark must warn: %s", out)
	}

	s.GitRemote = ""
	if out, err := run(s); err != nil || w.skipping("e2e-web") || !strings.Contains(out, "WARNING: could not read markers: no remote") {
		t.Fatalf("no remote: %v\n%s", err, out)
	}

	s = w.settings(settings.ModeCheck)
	s.Steps, s.Config = []string{"e2e-web"}, "no/such/lanes.yml"
	if out, err := run(s); err != nil || w.skipping("e2e-web") || !strings.Contains(out, "WARNING: could not read the lane config") {
		t.Fatalf("missing config: %v\n%s", err, out)
	}

	notRepo := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(notRepo))
	s = w.settings(settings.ModeCheck)
	s.Steps, s.Dir = []string{"e2e-web"}, notRepo
	if out, err := run(s); err != nil || !strings.Contains(out, "WARNING: could not read HEAD") {
		t.Fatalf("not a repository: %v\n%s", err, out)
	}
	if _, err := os.Stat(plugin.SkipFile(notRepo, "e2e-web")); err == nil {
		t.Fatal("not a repository: skipped")
	}
	s.Mode, s.Step = settings.ModeMark, "e2e-web"
	testgit.Write(t, notRepo, map[string]string{".lane/e2e-web.key": key.Key(nil) + "\n"})
	if out, err := run(s); err != nil || !strings.Contains(out, "WARNING: could not read HEAD") {
		t.Fatalf("mark outside a repository must warn, not fail: %v\n%s", err, out)
	}

	// A lane whose key cannot be computed runs, and leaves no key for mark;
	// the other steps of the same check are still decided.
	testgit.Write(t, w.dir, map[string]string{".woodpecker/lanes.yml": lanesYML + "  broken:\n    context: Dockerfile.gone\n    steps: [gone]\n"})
	testgit.Commit(t, w.dir, "a lane with a missing Dockerfile")
	w.check("e2e-web") // lanes.yml is in every key: mark the new web key
	w.mark("e2e-web")
	out = w.check("gone", "e2e-web")
	if !strings.Contains(out, "WARNING: could not compute the broken key") || w.skipping("gone") {
		t.Fatalf("an uncomputable key must warn and run:\n%s", out)
	}
	if _, err := os.Stat(plugin.KeyFile(w.dir, "gone")); err == nil {
		t.Fatal("an uncomputable key left a key file")
	}
	if !w.skipping("e2e-web") {
		t.Fatalf("one lane's broken key stopped another's skip:\n%s", out)
	}
	if out := w.mark("gone"); !strings.Contains(out, "WARNING: no key from check") {
		t.Fatalf("mark after an uncomputable key: %s", out)
	}

	// mark without check's key.
	fresh := newWS(t)
	if out := fresh.mark("e2e-web"); !strings.Contains(out, "WARNING: no key from check") {
		t.Fatalf("mark without a key: %s", out)
	}
	testgit.Write(t, fresh.dir, map[string]string{".lane/e2e-web.key": "not a key\n"})
	if out := fresh.mark("e2e-web"); !strings.Contains(out, "does not hold a key") {
		t.Fatalf("mark with a bad key: %s", out)
	}
}

func TestMarkWarnsWhenTheTagObjectCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only object store")
	}
	w := newWS(t)
	w.check("e2e-web")
	objects := filepath.Join(w.dir, ".git", "objects")
	chmodAll := func(mode os.FileMode) {
		_ = filepath.Walk(objects, func(p string, info os.FileInfo, err error) error {
			if err == nil && info.IsDir() {
				_ = os.Chmod(p, mode)
			}
			return nil
		})
	}
	chmodAll(0o555)
	t.Cleanup(func() { chmodAll(0o755) })
	if out := w.mark("e2e-web"); !strings.Contains(out, "WARNING: could not create the marker tag object") {
		t.Fatalf("a failing tag object must warn, not fail: %s", out)
	}
}

func TestWorkflowTyposFail(t *testing.T) {
	w := newWS(t)
	s := w.settings(settings.ModeCheck)
	if _, err := run(s); err == nil || !strings.Contains(err.Error(), "steps is required") {
		t.Fatalf("check without steps: %v", err)
	}
	s.Steps = []string{"build-web"}
	if _, err := run(s); err == nil || !strings.Contains(err.Error(), `step "build-web" is not in any lane`) {
		t.Fatalf("check of an unknown step: %v", err)
	}
	s.Steps = []string{"../x"}
	if _, err := run(s); err == nil {
		t.Fatal("check of an invalid step name succeeded")
	}
	s = w.settings(settings.ModeMark)
	if _, err := run(s); err == nil || !strings.Contains(err.Error(), "step is required") {
		t.Fatalf("mark without step: %v", err)
	}
	s.Step = "build-web"
	if _, err := run(s); err == nil || !strings.Contains(err.Error(), `step "build-web" is not in any lane`) {
		t.Fatalf("mark of an unknown step: %v", err)
	}
}

func TestPrune(t *testing.T) {
	w := newWS(t)
	noScratchLeft(t)
	testgit.Commit(t, w.dir, "history")
	w.check("e2e-web", "android-api-29")
	w.mark("e2e-web", func(s *settings.Settings) {
		s.Now = func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }
	})
	w.mark("android-api-29")
	keys := laneKeys(t, w.dir)
	oldRef, recentRef := plugin.MarkerRef("e2e-web", keys["web"]), plugin.MarkerRef("android-api-29", keys["android"])
	if w.markerSHA(oldRef) == "" || w.markerSHA(recentRef) == "" {
		t.Fatal("setup: markers missing")
	}

	s := w.settings(settings.ModePrune)
	s.Days, s.DryRun = "30", true
	out, err := run(s)
	if err != nil || !strings.Contains(out, "would delete "+oldRef) || strings.Contains(out, recentRef) || w.markerSHA(oldRef) == "" {
		t.Fatalf("dry run must name the old marker and delete nothing: %v\n%s", err, out)
	}
	if !strings.Contains(out, "1 of 2 markers are older than 30 days") {
		t.Fatalf("dry run count: %s", out)
	}

	s.DryRun = false
	if out, err := run(s); err != nil || w.markerSHA(oldRef) != "" || w.markerSHA(recentRef) == "" {
		t.Fatalf("prune must delete only markers older than the cutoff: %v\n%s", err, out)
	}
	if refs := testgit.Git(t, w.dir, "for-each-ref", "refs/lane-skip/"); refs != "" {
		t.Fatalf("prune left local refs:\n%s", refs)
	}
	w.fullClone("2")
	if out, err := run(s); err != nil || !strings.Contains(out, "0 of 1 markers") {
		t.Fatalf("prune with nothing old: %v\n%s", err, out)
	}
	s.Days = "soon"
	if _, err := run(s); err == nil {
		t.Fatal("prune without a number of days succeeded")
	}
	s.Days = "30"
	s.GitRemote = "file://" + filepath.Join(t.TempDir(), "no-such-remote.git")
	if _, err := run(s); err == nil {
		t.Fatal("prune against an unreachable remote succeeded")
	}
}

func TestVerify(t *testing.T) {
	w := newWS(t)
	out, err := run(w.settings(settings.ModeVerify))
	if err != nil || !strings.Contains(out, "lane web: steps e2e-web") || !strings.Contains(out, "lane unit: steps cargo-test") {
		t.Fatalf("verify of a good config: %v\n%s", err, out)
	}

	testgit.Write(t, w.dir, map[string]string{".woodpecker/lanes.yml": strings.Replace(lanesYML, "deploy/grafana", "deploy/grafna", 1) +
		"  broken:\n    context: Dockerfile.gone\n    steps: [gone]\n"})
	testgit.Commit(t, w.dir, "typos")
	_, err = run(w.settings(settings.ModeVerify))
	if err == nil || !strings.Contains(err.Error(), "input deploy/grafna does not exist in HEAD") ||
		!strings.Contains(err.Error(), "context Dockerfile.gone is not a file in HEAD") {
		t.Fatalf("verify of typos: %v", err)
	}

	testgit.Write(t, w.dir, map[string]string{".woodpecker/lanes.yml": "lanes:\n  web:\n    inptus: [e2e]\n"})
	testgit.Commit(t, w.dir, "unknown key")
	if _, err := run(w.settings(settings.ModeVerify)); err == nil {
		t.Fatal("verify of an unknown key succeeded")
	}
}

func TestFiles(t *testing.T) {
	w := newWS(t)
	s := w.settings(settings.ModeFiles)
	s.Lane = "web"
	out, err := run(s)
	if err != nil {
		t.Fatal(err)
	}
	files := strings.Split(strings.TrimSpace(out), "\n")
	has := map[string]bool{}
	for _, f := range files {
		has[f] = true
	}
	for _, want := range []string{"frontend/index.html", "crates/server/src/main.rs", "e2e/tests/a.spec.ts", "deploy/grafana/dashboard.json",
		".woodpecker/lanes.yml", ".woodpecker/web.yml", "Dockerfile.web", "Dockerfile.web.dockerignore", "scripts/check-image-metadata.sh"} {
		if !has[want] {
			t.Errorf("web covers no %s", want)
		}
	}
	for _, not := range []string{"android/app/build.gradle", "README.md", "docs/guide.md", "crates/server/tests/it.rs", "deploy/docker-compose.yml"} {
		if has[not] {
			t.Errorf("web covers %s", not)
		}
	}
	s.Lane = "prod"
	if _, err := run(s); err == nil {
		t.Fatal("files of an unknown lane succeeded")
	}
}

// The one error path that still skips: ls-remote lists the marker, so the key
// passed, but the marker itself cannot be fetched to label the skip.
func TestUnreadableMarkerStillSkips(t *testing.T) {
	w := newWS(t)
	noScratchLeft(t)
	w.check("e2e-web")
	w.mark("e2e-web", func(s *settings.Settings) { s.PipelineNum = "5" })

	attempts := 0
	restore := plugin.SetFetchMarker(func(dir, refspec string) error {
		attempts++
		return os.ErrPermission
	})
	out := w.check("e2e-web")
	restore()
	b, err := os.ReadFile(plugin.SkipFile(w.dir, "e2e-web"))
	if err != nil || !strings.Contains(string(b), "marker unreadable") || !strings.Contains(out, "SKIPPING e2e-web") {
		t.Fatalf("an unreadable marker must still skip: %v\n%s", err, out)
	}
	if attempts != 5 {
		t.Fatalf("fetched %d times, want 5 attempts", attempts)
	}
	if refs := testgit.Git(t, w.dir, "for-each-ref", "refs/lane-skip/"); refs != "" {
		t.Fatalf("left local refs:\n%s", refs)
	}

	// A fetch that fails twice, then works, labels the skip with the marker.
	attempts = 0
	restore = plugin.SetFetchMarker(func(dir, refspec string) error {
		if attempts++; attempts < 3 {
			return os.ErrPermission
		}
		return plugin.RealFetchMarker(w.remote, dir, refspec)
	})
	defer restore()
	w.check("e2e-web")
	b, _ = os.ReadFile(plugin.SkipFile(w.dir, "e2e-web"))
	if attempts != 3 || !strings.Contains(string(b), "  pipeline: 5") {
		t.Fatalf("after %d attempts the skip says:\n%s", attempts, b)
	}
}
