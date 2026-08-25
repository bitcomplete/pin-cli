# Maintain facts — pin-cli

Read by the `maintain` skill so each sweep skips rediscovery.

- **Repo:** `bitcomplete/pin-cli` (public) · maintainer `@terraboops`
- **What it is:** Go CLI for pin / pin-dash. Module `github.com/bitcomplete/pin-cli`
- **Ecosystem:** Go modules (`go.mod`), `go 1.25`

## Commands

| Purpose | Command |
|---|---|
| Build | `go build ./...` |
| Vet | `go vet ./...` |
| Test | `go test -count=1 ./...` |

Use `-count=1`: without it a cached pass looks like a real run.

## Merge convention

Squash merge. Delete the branch.

## Dependency cautions

- `go get -u ./...` rewrites the `go` directive (`1.25` → `1.25.0`). **Revert that** — it changes toolchain-selection semantics and isn't part of a dependency refresh.
- `go list -m -u all` reports `stretchr/testify` and `stretchr/objx` as updatable, but they are **not** in this module's requirements — they arrive through a dependency's own test tree. Nothing to bump.
- Real dependency surface is tiny: `zalando/go-keyring` plus its indirects (`wincred`, `godbus/dbus`, `golang.org/x/sys`).
- `go-keyring` touches platform credential stores (macOS Keychain, Windows wincred, D-Bus). Read its changelog before a major bump — breakage here is platform-specific and won't show up in CI on one OS.
