//go:build e2e

// Runs the built image the way Woodpecker does: the workspace mounted as the
// working directory, settings as PLUGIN_* variables.
//
//	PLUGIN_IMAGE=lane-skip:local go test -tags e2e ./test/e2e/
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/testgit"
)

func image(t *testing.T) string {
	img := os.Getenv("PLUGIN_IMAGE")
	if img == "" {
		t.Skip("PLUGIN_IMAGE not set")
	}
	return img
}

// plugin runs the image in dir (mounted at the same path) as the current user,
// so nothing it writes is left owned by root. Settings pass by name (-e NAME),
// so no value, the token included, is on docker's command line.
func plugin(t *testing.T, mount, dir string, env ...string) (string, error) {
	t.Helper()
	args := []string{"run", "--rm", "--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
		"-v", mount + ":" + mount, "-w", dir}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		args = append(args, "-e", name)
	}
	cmd := exec.Command("docker", append(args, image(t))...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestImageRoundTrip(t *testing.T) {
	image(t)
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	testgit.Git(t, repo, "init", "-q")
	testgit.Write(t, repo, map[string]string{
		".woodpecker/lanes.yml":       "lanes:\n  web:\n    context: Dockerfile.web\n    inputs: [e2e]\n    steps: [e2e-web]\n",
		"Dockerfile.web":              "FROM scratch\n",
		"Dockerfile.web.dockerignore": "docs\n",
		"src/app.js":                  "app\n",
		"e2e/a.spec":                  "spec\n",
		"docs/readme.md":              "docs\n",
	})
	testgit.Commit(t, repo, "snapshot")
	remote := "PLUGIN_GIT_REMOTE=file://" + filepath.Join(root, "remote.git")
	testgit.Git(t, root, "init", "-q", "--bare", "remote.git")

	guard := func() (skipped bool) {
		cmd := exec.Command("sh", "-c", `[ ! -e .lane/e2e-web.skip ] || { cat .lane/e2e-web.skip; exit 0; }; exit 3`)
		cmd.Dir = repo
		return cmd.Run() == nil
	}

	out, err := plugin(t, root, repo, "PLUGIN_MODE=verify")
	if err != nil || !strings.Contains(out, "lane web: steps e2e-web") {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	out, err = plugin(t, root, repo, "PLUGIN_MODE=check", "PLUGIN_STEPS=e2e-web", remote)
	if err != nil || guard() || !strings.Contains(out, "no green marker") {
		t.Fatalf("first check must run: %v\n%s", err, out)
	}
	out, err = plugin(t, root, repo, "PLUGIN_MODE=mark", "PLUGIN_STEP=e2e-web", "CI_PIPELINE_NUMBER=41", remote)
	if err != nil || !strings.Contains(out, "marked e2e-web green") {
		t.Fatalf("mark: %v\n%s", err, out)
	}
	out, err = plugin(t, root, repo, "PLUGIN_MODE=check", "PLUGIN_STEPS=e2e-web", remote)
	if err != nil || !guard() || !strings.Contains(out, "pipeline: 41") {
		t.Fatalf("second check must skip: %v\n%s", err, out)
	}
	out, err = plugin(t, root, repo, "PLUGIN_MODE=check", "PLUGIN_STEPS=e2e-web", "FULL_RUN=1", remote)
	if err != nil || guard() {
		t.Fatalf("FULL_RUN must run: %v\n%s", err, out)
	}
	out, err = plugin(t, root, repo, "PLUGIN_MODE=prune", "PLUGIN_DAYS=30", "PLUGIN_DRY_RUN=true", remote)
	if err != nil || !strings.Contains(out, "0 of 1 markers are older than 30 days") {
		t.Fatalf("prune: %v\n%s", err, out)
	}
	if out, err = plugin(t, root, repo, "PLUGIN_MODE=check", remote); err == nil {
		t.Fatalf("check without steps succeeded:\n%s", out)
	}
}

// Against this repository's real remote, from inside an actions/checkout
// workspace: its .git/config carries GitHub's own Authorization header, and
// the plugin's must replace it rather than be sent beside it.
func TestImageAgainstGitHub(t *testing.T) {
	image(t)
	workspace, repo, token := os.Getenv("GITHUB_WORKSPACE"), os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_TOKEN")
	if workspace == "" || repo == "" || token == "" {
		t.Skip("needs GITHUB_WORKSPACE, GITHUB_REPOSITORY and GITHUB_TOKEN (GitHub Actions)")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(workspace, ".lane")) })
	out, err := plugin(t, workspace, workspace, "PLUGIN_MODE=check", "PLUGIN_STEPS=e2e-smoke",
		"PLUGIN_CONFIG=test/e2e/testdata/lanes.yml", "CI_REPO="+repo, "PLUGIN_GITHUB_TOKEN="+token)
	if err != nil || strings.Contains(out, "WARNING") || !strings.Contains(out, "no green marker for e2e-smoke") {
		t.Fatalf("check against GitHub: %v\n%s", err, out)
	}
	if strings.Contains(out, token) {
		t.Fatal("the token reached the log")
	}
}
