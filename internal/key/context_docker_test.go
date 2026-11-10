//go:build docker

package key

// Holds Context to what BuildKit itself sends (v-note's
// scripts/test-lane-key-context.sh, generalised to every pattern case). A key
// covers the files its build context admits; if the matcher admitted fewer
// files than Docker sends, a change to one of the missing files would skip
// tests it can affect. Each case builds a throwaway image whose only job is to
// list the context BuildKit received through the case's ignore file, and
// requires that list to equal Context's. Needs a docker daemon with BuildKit:
//
//	go test -tags docker ./internal/key/

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/testgit"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/tree"
)

const listImage = "alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d"

const listDockerfile = `FROM ` + listImage + ` AS list
COPY . /ctx
RUN cd /ctx && find . \( -type f -o -type l \) | sed 's|^\./||' | LC_ALL=C sort > /files
FROM scratch
COPY --from=list /files /files
`

// Files with every shape a pattern can trip on: nesting, dotfiles, spaces,
// glob metacharacters in names, a symlink, near-miss prefixes.
var contextFiles = []string{
	"README.md", "CHANGELOG.md", "a.md", "abc.txt", "abd.txt", "ab.txt", "b.txt",
	".hidden", ".github/workflows/ci.yml", ".env",
	"docs/guide.md", "docs/sub/deep.md", "docs/sub/keep.txt", "docsx/file",
	"src/main.go", "src/lib/a.go", "src/lib/a_test.go", "src/vendor/v.go",
	"vendor/x.go", "node_modules/p/index.js", "node_modules/p/lib.js", "frontend/node_modules/q/index.js", "frontend/app.js",
	"keep/keep.txt", "keep/drop.txt", "x/y/z/deep.txt", "x/deep.txt",
	"dir with space/f.txt", "brackets[1].txt", "star*.txt", "q?.txt", "back\\slash.txt",
	"Dockerfile.other", "go.mod", "go.sum",
}

type contextCase struct {
	name     string
	specific *string // Dockerfile.ctx.dockerignore
	root     *string // .dockerignore
}

func p(s string) *string { return &s }

var contextCases = []contextCase{
	{name: "no ignore file"},
	{name: "empty", specific: p("")},
	{name: "comments and blanks", specific: p("# comment\n\n   \n  # not a comment: a pattern\n#*.md\n")},
	{name: "extension and re-admit", specific: p("*.md\n!README.md\n")},
	{name: "doublestar extension", specific: p("**/*.md\n")},
	{name: "directory excludes its contents", specific: p("docs\n")},
	{name: "slashes are normalised", specific: p("docs/\n/vendor\n./src/vendor\n//x//y\n")},
	{name: "allow-list", specific: p("*\n!src\n!go.mod\n!go.sum\n")},
	{name: "allow-list with wildcard exception", specific: p("*\n!src/**/*.go\n!**/*.txt\n")},
	{name: "re-admit a subdirectory", specific: p("src\n!src/lib\nsrc/lib/*_test.go\n")},
	{name: "re-admit a file under an excluded directory", specific: p("docs\n!docs/sub/keep.txt\n")},
	{name: "wildcard exception under an excluded directory", specific: p("node_modules\n!**/index.js\n")},
	{name: "nested node_modules", specific: p("**/node_modules\n")},
	{name: "star does not cross slash", specific: p("*/*.js\n*.txt\n")},
	{name: "question mark and classes", specific: p("ab?.txt\n[a-b].md\n[!a]*.txt\n")},
	{name: "escaped metacharacters", specific: p("brackets\\[1\\].txt\nstar\\*.txt\nq\\?.txt\n")},
	{name: "doublestar in the middle", specific: p("x/**/deep.txt\n")},
	{name: "doublestar alone", specific: p("**\n!go.mod\n")},
	{name: "last match wins", specific: p("keep\n!keep\nkeep/drop.txt\n!keep/drop.txt\nkeep/keep.txt\n")},
	{name: "space in a name and trailing whitespace", specific: p("dir with space   \n\t*.md\t\n")},
	{name: "crlf line endings", specific: p("*.md\r\n!README.md\r\nsrc\r\n")},
	{name: "dotfiles", specific: p(".*\n!.github\n")},
	{name: "the dockerfile and ignore file themselves", specific: p("Dockerfile*\n")},
	{name: "prefix is not a match", specific: p("docs\nab\n")},
	{name: "root ignore file is the fallback", root: p("*.md\nsrc\n")},
	{name: "specific ignore file wins over root", specific: p("vendor\n"), root: p("*\n")},
}

func TestContextMatchesBuildKit(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	files := map[string]string{"Dockerfile.ctx": listDockerfile}
	for _, f := range contextFiles {
		files[f] = f + "\n"
	}
	for _, tc := range contextCases {
		t.Run(tc.name, func(t *testing.T) {
			these := map[string]string{}
			for k, v := range files {
				these[k] = v
			}
			if tc.specific != nil {
				these["Dockerfile.ctx.dockerignore"] = *tc.specific
			}
			if tc.root != nil {
				these[".dockerignore"] = *tc.root
			}
			repo := testgit.Repo(t, these)
			if err := os.Remove(filepath.Join(repo, "README.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("a.md", filepath.Join(repo, "README.md")); err != nil {
				t.Fatal(err)
			}
			testgit.Commit(t, repo, "symlink")

			// Docker sees exactly the commit: no .git, nothing untracked.
			ctx := filepath.Join(t.TempDir(), "ctx")
			if err := os.Mkdir(ctx, 0o755); err != nil {
				t.Fatal(err)
			}
			archive := exec.Command("sh", "-c", `git -C "$1" archive HEAD | tar -x -C "$2"`, "sh", repo, ctx)
			if out, err := archive.CombinedOutput(); err != nil {
				t.Fatalf("export: %v\n%s", err, out)
			}
			out := filepath.Join(t.TempDir(), "out")
			build := exec.Command("docker", "build", "-q", "-f", filepath.Join(ctx, "Dockerfile.ctx"),
				"--output", "type=local,dest="+out, ctx)
			if b, err := build.CombinedOutput(); err != nil {
				t.Fatalf("docker build: %v\n%s", err, b)
			}
			raw, err := os.ReadFile(filepath.Join(out, "files"))
			if err != nil {
				t.Fatal(err)
			}
			var docker []string
			if trimmed := strings.TrimRight(string(raw), "\n"); trimmed != "" {
				docker = strings.Split(trimmed, "\n")
			}

			tr, err := tree.Read(repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := Context(tr, "Dockerfile.ctx")
			if err != nil {
				t.Fatal(err)
			}
			ours := paths(entries)
			sort.Strings(docker)
			sort.Strings(ours)
			if strings.Join(docker, "\n") != strings.Join(ours, "\n") {
				t.Fatalf("Context differs from BuildKit's\n%s", diff(docker, ours))
			}
		})
	}
}

func diff(docker, ours []string) string {
	in := func(list []string) map[string]bool {
		m := map[string]bool{}
		for _, s := range list {
			m[s] = true
		}
		return m
	}
	d, o := in(docker), in(ours)
	var b strings.Builder
	for _, s := range docker {
		if !o[s] {
			b.WriteString("- docker only: " + s + "\n")
		}
	}
	for _, s := range ours {
		if !d[s] {
			b.WriteString("+ matcher only: " + s + "\n")
		}
	}
	if b.Len() == 0 {
		b.WriteString("same set, different order:\n  docker: " + strings.Join(docker, ", ") + "\n  ours:   " + strings.Join(ours, ", "))
	}
	return b.String()
}
