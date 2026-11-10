# AGENTS.md

- Machine-wide rules: the agent-shared baseline (`~/.claude/CLAUDE.md` / `~/.codex/AGENTS.md`). This file overrides it where they differ.
- Trunk is `master`. Branch from it, open a PR, never stack branches.
- CI is **GitHub Actions** (`.github/workflows/ci.yml`), not Woodpecker: watch it with `gh pr checks` / `gh run watch`.
- Releases follow [RELEASING.md](RELEASING.md) (`release` workflow, `publish=true`). Never set the patch version or touch version tags by hand.
- Before changing what goes into a key, read `TestGoldenKey`: a key change needs a new header version.
- Tests: `go test ./...`; `go test -tags docker ./internal/key/` needs docker; `PLUGIN_IMAGE=... go test -tags e2e ./test/e2e/` runs the built image.
