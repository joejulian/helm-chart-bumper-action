# helm-chart-bumper-action

This repository contains a GitHub Action (published as a container image) and a small Go CLI.

## What it does (today)

The CLI bumps `Chart.yaml`'s `version` field based on semantic version changes detected between a base and current `Chart.yaml`:

- If any major change is detected: `version.major += 1`, reset minor/patch to 0
- Else if any minor change: `version.minor += 1`, reset patch to 0
- Else if any patch change: `version.patch += 1`
- Else: no change

Changes are computed from `appVersion` and `dependencies[*].version` (by dependency name).

## Action image

GoReleaser publishes the action image to:

- `ghcr.io/joejulian/actions/helm-chart-bumper-action:<tag>`
- `ghcr.io/joejulian/actions/helm-chart-bumper-action:latest`

`action.yml` points at the `latest` tag.

## Development

```bash
go test ./...
go build ./cmd/helm-chart-bumper
```
