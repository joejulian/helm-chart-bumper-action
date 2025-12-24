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
