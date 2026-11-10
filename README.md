# woodpecker-plugin-lane-skip

Woodpecker CI plugin that skips an expensive test step when exactly the files it tests have already passed. A docs-only push skips the emulators; a change to anything a step reads runs it.

[![CI](https://github.com/vcheesbrough/woodpecker-plugin-lane-skip/actions/workflows/ci.yml/badge.svg)](https://github.com/vcheesbrough/woodpecker-plugin-lane-skip/actions/workflows/ci.yml)

Docker layer caching cannot skip a run-time test (an emulator, a compose stack), so this is the one test-skipping mechanism: images are still built every pipeline, only the tests are skipped.

## License

Source-available under the [PolyForm Noncommercial License 1.0.0](LICENSE). This is **not** an OSI-approved open source license.

**Commercial use** requires a separate license. See [LICENSE-TIER.md](LICENSE-TIER.md).

## How it works

- **Lane**: a set of files and the test steps that pass or fail on them, declared in `.woodpecker/lanes.yml`.
- **Key**: a sha256 over the header `lane-skip key v1` and one `<mode> <blob> <path>` line per covered file, sorted in byte order (`LC_ALL=C`), read from `git ls-tree -r HEAD`. It is the commit's content, never the working tree: an uncommitted edit, the release tag or the pipeline number never change it. A commit whose covered files are unchanged has the same key, whatever else changed.
- **Covered files** of a lane:
  - its build context, when it has one: every tracked file Docker would send for `docker build -f <context> .`, through the ignore file BuildKit would use (`<context>.dockerignore`, else the root `.dockerignore`), read from HEAD and matched with Docker's own matcher (`moby/patternmatcher`). Derived, never listed, so a file added to the context is in the key by construction. A test holds this to a real BuildKit `COPY .` listing for every pattern case;
  - its declared `inputs`: files, or directories and everything under them, that its steps read from outside the context;
  - implicitly: the lane config itself, the Dockerfile and the ignore file in use.
- **Marker**: an annotated tag object at `refs/ci/green/<step>/<key>` on the remote, recording the commit, branch, pipeline number and URL that passed. It is pushed create-only, so a marker is never overwritten. Each step has its own marker: a pass on API 29 says nothing about API 36.

## `.woodpecker/lanes.yml`

```yaml
lanes:
  web:                                 # lane name: letters, digits, '.', '_', '-'
    context: Dockerfile.web            # optional; the build context is the repository root
    inputs: [e2e, deploy/grafana]      # optional; files or directories
    steps: [e2e-web]                   # required; each step is in exactly one lane
  android:
    context: Dockerfile.android
    inputs: [.woodpecker/android.yml, scripts/android-build-box-image.ref]
    steps: [android-api-29, android-api-36]
  unit:                                # no context: keyed only on its inputs
    inputs: [crates, Cargo.toml, Cargo.lock]
    steps: [cargo-test]
```

Unknown keys are refused, so a typo cannot silently drop an input. Paths are relative to the repository root (`./e2e/` is `e2e`). Step names become ref components and file names, so they are letters, digits, `.`, `_` and `-`. Editing `lanes.yml` changes every lane's key once. Run `mode: verify` in a checks pipeline to fail on an input or context that does not exist.

## Wiring

A plugin cannot skip another step, so the decision travels through the workspace, like `.release-tag`. Three parts:

```yaml
steps:
  # 1. check: writes .lane/<step>.key, and .lane/<step>.skip when that key already passed.
  lanes:
    image: &lane-skip ghcr.io/vcheesbrough/woodpecker-plugin-lane-skip:<version>@sha256:<digest>  # from the GitHub release
    settings:
      mode: check
      steps: [e2e-web]
      github_token: { from_secret: github_token }   # only for a private repo

  # 2. the skip guard, as the test step's first command (quoted: YAML reads a bare `[` as a list).
  e2e-web:
    depends_on: [lanes, build-web]
    image: docker:cli
    commands:
      - '[ ! -e .lane/e2e-web.skip ] || { cat .lane/e2e-web.skip; exit 0; }'
      - ./run-e2e.sh

  # 3. mark: in its own step, after the tests passed. Marks the key check computed, before the tests ran.
  mark-e2e-web:
    depends_on: [e2e-web]
    image: *lane-skip
    settings:
      mode: mark
      step: e2e-web
      github_token: { from_secret: github_token }   # needs contents: write
```

- One `check` step can decide several steps (`steps: [android-api-29, android-api-36]`); give each test step its own guard and its own `mark` step.
- `mark` depends on the test step, so it runs only when the tests passed (or were skipped, when it reports `already marked`).
- `FULL_RUN` set to anything but empty, `0`, `false`, `no` or `off` (a manual pipeline variable, or the `full_run` setting) makes `check` run every step. It still writes the key, so `mark` records the pass.
- Add `.lane` to `.gitignore`, and to the ignore file of any build whose context is the workspace, so the decision files never reach an image.

### Failure behaviour

| Mode | Fails (exit 1) when | Otherwise, on any error |
|---|---|---|
| `check` | no `steps`, or a step no lane declares (a workflow typo) | warns and **runs**: no skip file is written. Never skips on doubt. A stale skip file is removed first. |
| `mark` | no `step`, or a step the lane config does not declare | warns and exits 0: the tests passed, and a missing marker only costs one re-run. |
| `prune` | `days` is not a whole number, or the markers cannot be read or deleted | |
| `verify` | the config does not parse, or names a context or input HEAD does not have | |

## Modes and settings

| `mode` | Does |
|---|---|
| `check` | For each of `steps`: writes `.lane/<step>.key`; if `refs/ci/green/<step>/<key>` exists, writes `.lane/<step>.skip` holding the marker's message. One `ls-remote` for all steps. |
| `mark` | Reads `.lane/<step>.key` and creates `refs/ci/green/<step>/<key>`, create-only. |
| `prune` | Deletes markers whose tag is older than `days`; `dry_run` lists them instead. Needs a clone as its workspace. |
| `verify` | Checks the config against HEAD and prints each lane's file count and key. |
| `files` | Prints every file `lane`'s key covers, one per line, for a repository's own checks of its lanes. |

| Setting | Used by | Default |
|---|---|---|
| `mode` | all | required |
| `config` | all | `.woodpecker/lanes.yml` (read from HEAD) |
| `steps` | check | required: list, or comma-separated |
| `step` | mark | required |
| `full_run` | check | the `FULL_RUN` environment variable |
| `days` | prune | required |
| `dry_run` | prune | `false` |
| `lane` | files | required |
| `github_token` | check, mark, prune | the `GITHUB_TOKEN` environment variable; reading a public repo needs none, `mark` and `prune` need contents write |
| `git_remote` | check, mark, prune | `https://github.com/<CI_REPO>.git` |

The marker message names `CI_COMMIT_BRANCH`, `CI_PIPELINE_NUMBER` and `CI_PIPELINE_URL`. The token travels in a git HTTP header scoped to the remote URL, never in a URL or on a command line, and replaces any header the checkout configured.

## Markers and pruning

Markers live under `refs/ci/green/`, outside `refs/tags/` and `refs/heads/`, so clones and fetches never download them. List them with `git ls-remote origin 'refs/ci/green/*'`; read one with `git fetch origin refs/ci/green/<step>/<key> && git cat-file -p FETCH_HEAD`.

CI never deletes a marker, so the namespace grows by up to one ref per step per pipeline whose inputs changed. Deleting a marker is always safe: it only costs that step one re-run. Prune from a cron pipeline:

```yaml
when:
  - event: cron
    cron: prune-lane-markers
steps:
  prune:
    image: ghcr.io/vcheesbrough/woodpecker-plugin-lane-skip:<version>@sha256:<digest>
    settings:
      mode: prune
      days: 30
      github_token: { from_secret: github_token }
```

## GitHub Actions

The repo is also a container action; settings become `with:` inputs and GitHub's own variables label the marker.

```yaml
- uses: vcheesbrough/woodpecker-plugin-lane-skip@<version>
  with: { mode: check, steps: unit, github_token: "${{ secrets.GITHUB_TOKEN }}" }
- run: '[ ! -e .lane/unit.skip ] || { cat .lane/unit.skip; exit 0; }; go test ./...'
- uses: vcheesbrough/woodpecker-plugin-lane-skip@<version>
  with: { mode: mark, step: unit, github_token: "${{ secrets.GITHUB_TOKEN }}" }   # needs contents: write
```

## Development

```bash
go test ./...                                  # unit and mode tests: real git against local bare repos
go test -tags docker ./internal/key/           # the context matcher against a real BuildKit listing (needs docker)
docker build -t lane-skip:local .
PLUGIN_IMAGE=lane-skip:local go test -tags e2e ./test/e2e/   # the image, run the way Woodpecker runs it
```

The key format is pinned by `TestGoldenKey`: changing what goes into a key means bumping the header's version, so no old marker can match a key computed under different rules.

This repo releases itself through GitHub Actions; see [RELEASING.md](RELEASING.md).
