package gitremote

import (
	"encoding/base64"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/testgit"
)

func repoAndRemote(t *testing.T) (work, commit string, r Remote) {
	work = testgit.Repo(t, map[string]string{"a": "a\n"})
	commit = testgit.Git(t, work, "rev-parse", "HEAD")
	return work, commit, Remote{URL: testgit.Bare(t)}
}

func TestCreateRefIsCreateOnly(t *testing.T) {
	work, commit, remote := repoAndRemote(t)
	ref := "refs/ci/green/step/k"
	first, err := remote.MakeTag(work, commit, "ci/green/step/k", "pipeline 1\n", time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.CreateRef(work, first, ref); err != nil {
		t.Fatal(err)
	}
	// Same commit, a later pipeline: a distinct object, so the push must be
	// refused rather than replace the marker.
	second, err := remote.MakeTag(work, commit, "ci/green/step/k", "pipeline 2\n", time.Unix(200, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.CreateRef(work, second, ref); err == nil {
		t.Fatal("second create of an existing ref succeeded")
	}
	got := testgit.Git(t, work, "ls-remote", remote.URL, ref)
	if !strings.HasPrefix(got, first) {
		t.Fatalf("ref is %q, want it to stay at %s", got, first)
	}
	found, err := remote.Existing(ref, "refs/ci/green/step/other")
	if err != nil {
		t.Fatal(err)
	}
	if !found[ref] || found["refs/ci/green/step/other"] {
		t.Fatalf("Existing = %v", found)
	}
}

func TestMakeTagCreatesNoLocalRef(t *testing.T) {
	work, commit, remote := repoAndRemote(t)
	if _, err := remote.MakeTag(work, commit, "ci/green/s/k", "m\n", time.Now()); err != nil {
		t.Fatal(err)
	}
	if refs := testgit.Git(t, work, "for-each-ref", "refs/tags/"); refs != "" {
		t.Fatalf("local refs: %s", refs)
	}
}

func TestRedactHidesToken(t *testing.T) {
	r := Remote{Token: "s3cret"}
	if got := r.redact("fatal: bad header s3cret"); got != "fatal: bad header ***" {
		t.Fatalf("got %q", got)
	}
}

func TestCommandScopesTheTokenAndKeepsInheritedConfig(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "http.sslVerify")
	t.Setenv("GIT_CONFIG_VALUE_0", "true")
	r := Remote{URL: "https://github.com/owner/repo.git", Token: "s3cret"}
	cmd := r.command("", "ls-remote", r.URL)

	for _, arg := range cmd.Args {
		if strings.Contains(arg, "s3cret") {
			t.Fatalf("token in argv: %v", cmd.Args)
		}
	}
	want := []string{
		"GIT_CONFIG_COUNT=3",
		"GIT_CONFIG_KEY_1=http.https://github.com/owner/repo.git.extraHeader",
		"GIT_CONFIG_VALUE_1=", // clears headers from the checkout's own config
		"GIT_CONFIG_KEY_2=http.https://github.com/owner/repo.git.extraHeader",
		"GIT_CONFIG_VALUE_2=Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:s3cret")),
	}
	for _, kv := range want {
		if !slices.Contains(cmd.Env, kv) {
			t.Errorf("missing %s", kv)
		}
	}
	if !slices.Contains(cmd.Env, "GIT_CONFIG_KEY_0=http.sslVerify") {
		t.Error("inherited GIT_CONFIG_KEY_0 was dropped")
	}
}

func TestCommandWithoutTokenAddsNoHeader(t *testing.T) {
	t.Setenv("GIT_CONFIG_COUNT", "")
	cmd := Remote{URL: "https://github.com/owner/repo.git"}.command("", "ls-remote")
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") && kv != "GIT_CONFIG_COUNT=" {
			t.Fatalf("set %s without a token", kv)
		}
	}
}

// actions/checkout stores its own Authorization header in the workspace's
// .git/config, and GitHub refuses a request carrying two. This serves a real
// repository over smart HTTP and records what git actually sends.
func TestOnlyThePluginsAuthorizationHeaderIsSent(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	work, _, _ := repoAndRemote(t)
	bare := filepath.Join(t.TempDir(), "served.git")
	testgit.Git(t, work, "clone", "-q", "--bare", work, bare)

	var mu sync.Mutex
	var seen [][]string
	backend := &cgi.Handler{
		Path: gitPath,
		Args: []string{"http-backend"},
		Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(bare), "GIT_HTTP_EXPORT_ALL=1"},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Values("Authorization"))
		mu.Unlock()
		backend.ServeHTTP(w, r)
	}))
	defer srv.Close()

	testgit.Git(t, work, "config", "http."+srv.URL+"/.extraheader", "AUTHORIZATION: basic Y2hlY2tvdXQ=")
	t.Chdir(work)

	remote := Remote{URL: srv.URL + "/" + filepath.Base(bare), Token: "plugin-token"}
	if _, err := remote.Existing("refs/heads/master"); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:plugin-token"))
	if len(seen) == 0 {
		t.Fatal("server saw no requests")
	}
	for i, headers := range seen {
		if len(headers) != 1 || headers[0] != want {
			t.Fatalf("request %d sent Authorization %q, want exactly [%q]", i, headers, want)
		}
	}
}
