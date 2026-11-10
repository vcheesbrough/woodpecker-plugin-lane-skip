// Package testgit builds scratch repositories for tests: real git, local bare
// remotes, nothing on the network.
package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Git runs git in dir and fails the test on error.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := Try(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(out)
}

// Try runs git in dir and returns its combined output.
func Try(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=t", "-c", "user.email=t@t.invalid", "-c", "init.defaultBranch=master",
		"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false",
	}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Repo creates a repository holding files and commits them.
func Repo(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	Git(t, dir, "init", "-q")
	Write(t, dir, files)
	Commit(t, dir, "snapshot")
	return dir
}

// Write writes files (path -> content) under dir.
func Write(t testing.TB, dir string, files map[string]string) {
	t.Helper()
	for p, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Append adds a line to a file under dir.
func Append(t testing.TB, dir, p string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(p)), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString("# lane-skip test\n"); err != nil {
		t.Fatal(err)
	}
}

// Commit stages everything and commits, allowing an empty commit.
func Commit(t testing.TB, dir, msg string) {
	t.Helper()
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
}

// Bare creates an empty bare repository and returns its file:// URL.
func Bare(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	Git(t, filepath.Dir(dir), "init", "-q", "--bare", dir)
	return "file://" + dir
}
