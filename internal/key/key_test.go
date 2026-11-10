package key

import (
	"reflect"
	"testing"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/lanes"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/testgit"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

// The key format is a contract with every marker already on a remote: a key
// that changes under the same files silently re-runs every step once, and one
// computed under different rules must never match an old marker. If this
// test fails, bump Header's version deliberately and update the golden values.
func TestGoldenKey(t *testing.T) {
	if Header != "lane-skip key v1\n" {
		t.Fatalf("Header = %q; a changed header needs new golden values", Header)
	}
	entries := []tree.Entry{
		{Mode: "100755", Object: "2222222222222222222222222222222222222222", Path: "b/run.sh"},
		{Mode: "100644", Object: "1111111111111111111111111111111111111111", Path: "a.txt"},
		{Mode: "120000", Object: "3333333333333333333333333333333333333333", Path: "B-link"},
	}
	// Equal to `printf "lane-skip key v1\n<the lines>" | sha256sum`, the lines
	// in LC_ALL=C order of path, so "B-link" sorts before "a.txt".
	const want = "2b707260d00c1ce286d1ca76226fdb1859c583304816e4d62ed645443957d42c"
	if got := Key(entries); got != want {
		t.Fatalf("Key = %s, want %s", got, want)
	}

	// The same through real git: blob ids are content addresses, so a fixed
	// set of files has a fixed key on any machine.
	dir := testgit.Repo(t, map[string]string{
		".woodpecker/lanes.yml": "lanes:\n  unit:\n    inputs: [src]\n    steps: [test]\n",
		"src/main.go":           "package main\n",
		"docs/readme.md":        "not covered\n",
	})
	tr, err := tree.Read(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(tr, lanes.DefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Lane(tr, cfg, cfg.Lanes["unit"])
	if err != nil {
		t.Fatal(err)
	}
	const wantRepo = "33f643cce41fb0756e12fe64d65bf13d87ebf297f6dbe35c74b54cf82d80bf4f"
	if got != wantRepo {
		t.Fatalf("repo key = %s, want %s", got, wantRepo)
	}
}

func TestValid(t *testing.T) {
	if !Valid(Key(nil)) {
		t.Fatal("a key is not Valid")
	}
	for _, bad := range []string{"", "abc", "2B707260D00C1CE286D1CA76226FDB1859C583304816E4D62ED645443957D42C", "zz" + Key(nil)[2:]} {
		if Valid(bad) {
			t.Errorf("Valid(%q)", bad)
		}
	}
}

func paths(entries []tree.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func TestIgnoreFileFollowsBuildKit(t *testing.T) {
	dir := testgit.Repo(t, map[string]string{"Dockerfile": "", "web.Dockerfile": "", "web.Dockerfile.dockerignore": "", ".dockerignore": ""})
	tr, _ := tree.Read(dir, "HEAD")
	if got := IgnoreFile(tr, "web.Dockerfile"); got != "web.Dockerfile.dockerignore" {
		t.Errorf("specific: %q", got)
	}
	if got := IgnoreFile(tr, "Dockerfile"); got != ".dockerignore" {
		t.Errorf("fallback: %q", got)
	}
	bare := testgit.Repo(t, map[string]string{"Dockerfile": ""})
	tr, _ = tree.Read(bare, "HEAD")
	if got := IgnoreFile(tr, "Dockerfile"); got != "" {
		t.Errorf("none: %q", got)
	}
}

func TestCoveredAddsInputsAndImplicitFiles(t *testing.T) {
	dir := testgit.Repo(t, map[string]string{
		".woodpecker/lanes.yml":       "lanes:\n  web:\n    context: Dockerfile.web\n    inputs: [e2e, deploy/grafana/d.json]\n    steps: [e2e-web]\n",
		".woodpecker/web.yml":         "",
		"Dockerfile.web":              "",
		"Dockerfile.web.dockerignore": "*\n!src\n",
		"src/a.rs":                    "",
		"e2e/t.spec":                  "",
		"e2e2/not-an-input":           "",
		"deploy/grafana/d.json":       "",
		"deploy/grafana/other.json":   "",
		"docs/x.md":                   "",
	})
	tr, _ := tree.Read(dir, "HEAD")
	cfg, err := LoadConfig(tr, lanes.DefaultPath)
	if err != nil {
		t.Fatal(err)
	}
	covered, err := Covered(tr, cfg, cfg.Lanes["web"])
	if err != nil {
		t.Fatal(err)
	}
	want := []string{".woodpecker/lanes.yml", "Dockerfile.web", "Dockerfile.web.dockerignore", "deploy/grafana/d.json", "e2e/t.spec", "src/a.rs"}
	if got := paths(covered); !reflect.DeepEqual(got, want) {
		t.Fatalf("covered = %v\nwant      %v", got, want)
	}
}

func TestContextIsRequiredToExist(t *testing.T) {
	dir := testgit.Repo(t, map[string]string{".woodpecker/lanes.yml": "lanes:\n  web:\n    context: Dockerfile.web\n    steps: [s]\n"})
	tr, _ := tree.Read(dir, "HEAD")
	cfg, _ := LoadConfig(tr, lanes.DefaultPath)
	if _, err := Covered(tr, cfg, cfg.Lanes["web"]); err == nil {
		t.Fatal("a missing Dockerfile computed a key")
	}
}

func TestBadPatternIsAnError(t *testing.T) {
	dir := testgit.Repo(t, map[string]string{"Dockerfile": "", "Dockerfile.dockerignore": "[\n"})
	tr, _ := tree.Read(dir, "HEAD")
	if _, err := Context(tr, "Dockerfile"); err == nil {
		t.Fatal("a malformed pattern matched")
	}
}
