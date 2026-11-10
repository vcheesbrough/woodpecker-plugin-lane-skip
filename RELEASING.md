# Releasing

This repository releases through **GitHub Actions**, with versions from **woodpecker-plugin-release-versions** (0.2.0, pinned by digest in `.github/workflows/release.yml`), so it follows the agent-shared `release-flow` contract: one run builds and tests one image once, publishes it as `ghcr.io/vcheesbrough/woodpecker-plugin-lane-skip:<version>` only (no `:latest`, no candidate tag), and the git tag means "this build was released".

## Release line (Go)

`internal/version/version.go` carries `MAJOR.MINOR.0`:

```go
const Version = "0.1.0" // the release workflow claims the patch; never set it by hand
```

Bump `MAJOR.MINOR` there to start a new line.

## Release flow

**Actions → release → Run workflow** on `master`, and set **publish**:

1. **version** claims the next patch on the line (`refs/builds/<version>`) and writes `.release-tag`.
2. `go test`, the BuildKit context comparison, the image build (with `org.opencontainers.image.version`, `.revision` and `.source` labels) and the image smoke test.
3. Only with **publish = true**: **publish** pushes `ghcr.io/vcheesbrough/woodpecker-plugin-lane-skip:<version>`, **tag** pushes the annotated git tag, and a GitHub Release is created whose body names the image by digest.

| `publish` | Result |
|---|---|
| **false** | Build and test only. The claimed version is used up and becomes a gap. |
| **true** | Image, git tag and GitHub Release. |

A failed run also uses its number up. Never create, move or delete a version tag or `refs/builds/*` by hand, and never re-push an image.

The ghcr package must stay **public**: Woodpecker pulls the image anonymously.

## CI

On every pull request and push to `master` (`.github/workflows/ci.yml`): gofmt, `go vet`, `go test -race`, the context matcher against BuildKit, an image build and the image smoke test.
