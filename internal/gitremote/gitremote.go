// Package gitremote talks to the repository the markers live in. URL may be
// any git URL, so the tests drive real git against local bare repositories.
package gitremote

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Remote struct {
	URL   string
	Token string
}

// command runs git with the token in an HTTP header passed through
// GIT_CONFIG_* environment variables: never in the URL, where git echoes it in
// errors, and never in argv, where any process listing shows it. The header is
// scoped to the remote's URL, so a redirect or another host never receives
// it, and it is appended after any GIT_CONFIG_* entries CI already set.
//
// An empty value first clears any extra headers git already collected: the
// workspace is often a checkout whose .git/config holds the CI's own
// Authorization header (actions/checkout does this), and GitHub refuses a
// request carrying two. (The same approach as woodpecker-plugin-release-versions.)
func (r Remote) command(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-c", "safe.directory=*"}, args...)...)
	if dir != "" {
		cmd.Dir = dir
		if abs, err := filepath.Abs(dir); err == nil {
			cmd.Dir = abs
		}
	}
	env := append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if r.Token != "" {
		n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.Token))
		key := "http." + r.URL + ".extraHeader"
		env = append(env,
			fmt.Sprintf("GIT_CONFIG_COUNT=%d", n+2),
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n, key),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=", n),
			fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", n+1, key),
			fmt.Sprintf("GIT_CONFIG_VALUE_%d=Authorization: Basic %s", n+1, basic),
		)
	}
	cmd.Env = env
	return cmd
}

func (r Remote) run(dir, stdin string, args ...string) (string, error) {
	cmd := r.command(dir, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %s: %w", args[0], r.redact(strings.TrimSpace(string(out))), err)
	}
	return string(out), nil
}

func (r Remote) redact(s string) string {
	if r.Token == "" {
		return s
	}
	return strings.ReplaceAll(s, r.Token, "***")
}

// Existing returns which of the given refs exist on the remote, in one
// round trip.
func (r Remote) Existing(refs ...string) (map[string]bool, error) {
	out, err := r.run("", "", append([]string{"ls-remote", r.URL}, refs...)...)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, ref := range refs {
		want[ref] = true
	}
	found := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok && want[ref] {
			found[ref] = true
		}
	}
	return found, nil
}

// MakeTag writes an annotated tag object for commit into the repository at
// dir and returns its id. No local ref is created, so steps marking at once in
// one shared clone cannot collide; the object reaches the remote only through
// CreateRef.
func (r Remote) MakeTag(dir, commit, name, message string, when time.Time) (string, error) {
	body := fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger woodpecker-plugin-lane-skip <lane-skip@localhost> %d +0000\n\n%s",
		commit, name, when.Unix(), message)
	out, err := r.run(dir, body, "mktag")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CreateRef creates ref at object on the remote, create-only: an empty lease
// is "the ref must not exist", checked by the server. A plain push is not
// enough — a newer marker's commit can be a fast-forward of the old one's.
func (r Remote) CreateRef(dir, object, ref string) error {
	_, err := r.run(dir, "", "push", "--porcelain", "--force-with-lease="+ref+":", r.URL, object+":"+ref)
	return err
}

// Fetch fetches refspec into the repository at dir. --depth=1 keeps it to the
// tag objects and their commits, never the commits' history.
func (r Remote) Fetch(dir, refspec string) error {
	_, err := r.run(dir, "", "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--depth=1", r.URL, refspec)
	return err
}

// Delete removes refs from the remote in one push.
func (r Remote) Delete(dir string, refs ...string) error {
	if len(refs) == 0 {
		return nil
	}
	_, err := r.run(dir, "", append([]string{"push", "--porcelain", r.URL, "--delete"}, refs...)...)
	return err
}
