# helm-chart-bumper-action

This repository contains:

- a **small Go CLI** (`helm-chart-bumper`)
- a **GitHub Action** that runs the CLI as a container image

The action is designed to be **matrix-friendly**, **side-effect minimal**, and suitable for **PR-per-chart workflows**.

---

## What it does

`helm-chart-bumper` updates a Helm chart’s `Chart.yaml` **`version` field** based on semantic version changes detected between a *base* and *current* chart.

### Change detection rules

Changes are detected from:

- `appVersion`
- `dependencies[*].version` (matched by dependency name)

The resulting version bump logic:

| Detected change | Resulting bump |
|---------------|----------------|
| Any **major** change | `version.major += 1`, reset minor & patch to `0` |
| Any **minor** change | `version.minor += 1`, reset patch to `0` |
| Any **patch** change | `version.patch += 1` |
| No change | no version update |

---

## Base comparison (git in-memory)

The base `Chart.yaml` can be read **directly from git**, without writing temp files or mounting `/tmp`.

This is done using `go-git` and works entirely in-memory.

You can compare against:

- `origin/main`
- `main`
- `HEAD`
- `HEAD~1`
- any valid git ref that exists in the checkout

---

## CLI usage

```bash
helm-chart-bumper \
  (--base path/to/base/Chart.yaml | \
   --base-ref <git-ref> [--base-ref-path path/in/repo/Chart.yaml]) \
  --cur path/to/cur/Chart.yaml \
  [--repo path/to/repo] \
  [--write]
```

### Flags

| Flag | Description |
|----|------------|
| `--base` | Path to a base `Chart.yaml` on disk |
| `--base-ref` | Git ref to read the base `Chart.yaml` from |
| `--base-ref-path` | Repo-relative path at that ref (defaults to `--cur`) |
| `--cur` | Path to the current `Chart.yaml` (required) |
| `--repo` | Git working tree root (default `"."`) |
| `--write` | Write the updated `Chart.yaml` back to disk |

### Behavior

| Mode | Effect |
|----|------|
| no `--write` | Render updated `Chart.yaml` to **stdout** (dry-run) |
| `--write` | Update file **in place**, produce no stdout |

---

## GitHub Action behavior

### Outputs

The action exposes **one output**:

```yaml
changed: "true" | "false"
```

- `changed=true` **only if** `--write` caused bytes to be written to disk
- `changed=false` otherwise

This guarantees:
- no empty PRs
- no guessing in workflows
- no `git diff` hacks required

---

## Example: PR-per-chart (matrix)

```yaml
- name: Bump chart
  id: bump
  uses: joejulian/helm-chart-bumper-action@v0
  with:
    base_ref: origin/main
    cur: charts/home-assistant/Chart.yaml
    write: "true"

- name: Create PR
  if: steps.bump.outputs.changed == 'true'
  uses: peter-evans/create-pull-request@v6
  with:
    branch: automation/bump-home-assistant
    title: "chore(home-assistant): bump chart"
```

---

## Action image

The action is published as a container image via GoReleaser:

```
ghcr.io/joejulian/actions/helm-chart-bumper-action:<tag>
```

Recommended tags:

- `v0.x.y` — immutable release
- `v0` — moving major tag (no-maintenance workflows)

---

## Development

```bash
go test ./...
go build ./cmd/helm-chart-bumper
```

---

## Design goals

- No git binary required in the container
- No shell scripts inside the action
- No temp files or host-path coupling
- Matrix-friendly
- Explicit, machine-readable outputs

---

## Optional: update images and dependencies before bumping the chart version

`helm-chart-bumper` can optionally update:

- **container image versions** (by consulting container registries)
- **Helm chart dependencies** in `Chart.yaml` (by consulting Helm repositories)

Then it re-computes the semver change level and applies the appropriate bump to `Chart.yaml:version`.

### Flags

| Flag | Description |
|----|------------|
| `--update-images` | Scan YAML files for `# bump:` directives and update the immediately following scalar key |
| `--update-deps` | Update `Chart.yaml dependencies[].version` to the latest available versions |
| `--scan-glob` | Comma-separated glob(s) (relative to the chart directory) to scan for directives (default: `Chart.yaml,values*.yaml`) |

### Image update directives

To update an image version, add a directive comment **immediately above** the YAML key that stores the version you want updated.

**Rules**

- The directive applies to the **next non-empty, non-comment YAML line**.
- The next YAML line **must** be a **scalar assignment** on a single line (e.g. `appVersion: "2.3.1"`, `tag: "1.2.3"`).
- The directive is **file-local**; the tool updates the key in the same file where the directive appears.
- `image=` is **required** and must be the **full repository path**, including registry host (examples below). No implicit `docker.io`.

**Directive format**

```yaml
# bump: image=<full-image-repo> strategy=<semver|regex|literal|digest> [constraint="<semver constraint>"] [tagRegex="<regex>"] [allowPrerelease=<true|false>] [platform=<os/arch>]
<key>: "<current value>"
```

#### Example: update `Chart.yaml appVersion` from an image registry

```yaml
# bump: image=ghcr.io/example/myapp strategy=semver constraint="^2.0.0"
appVersion: "2.3.1"
```

#### Example: update a values file image tag

```yaml
image:
  repository: ghcr.io/example/myapp
  # bump: image=ghcr.io/example/myapp strategy=semver
  tag: "2.3.1"
```

#### Example: update a digest from a sibling `tag`

```yaml
image:
  repository: ghcr.io/example/myapp
  # bump: image=ghcr.io/example/myapp strategy=semver
  tag: "2.3.1"
  # bump: image=ghcr.io/example/myapp strategy=digest platform=linux/amd64
  digest: "sha256:..."
```

### Dependency updates

When `--update-deps` is enabled, `helm-chart-bumper` resolves the latest versions of `Chart.yaml` dependencies by downloading each dependency repository's `index.yaml` and selecting the newest matching semver.

- If `dependencies[].version` is a semver constraint, the selected version must satisfy it.
- If it is not a constraint, the selected version is simply the highest semver available.

