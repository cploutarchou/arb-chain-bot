# Development container

A ready-made environment holding every tool this repository's CI runs,
at the versions CI runs them. Open the folder in VS Code with the Dev
Containers extension and choose **Reopen in Container**, or from a
terminal:

```bash
npx @devcontainers/cli up --workspace-folder .
npx @devcontainers/cli exec --workspace-folder . bash
```

Nothing here changes how the platform behaves. Live execution stays
disabled, and the container holds no exchange credentials — those live
only in the vault's write-only `exchange` group.

## What is inside

| Tool | Version | Why it is pinned there |
|---|---|---|
| Go | 1.25.14 | the `toolchain` line in `go.mod` |
| golangci-lint | 2.5.0 | `.github/workflows/ci.yml` runs `v2.5` |
| Node | 22 | `.github/workflows/ci.yml` `node-version: 22` |
| gitleaks | 8.30.1 | the `secrets` CI job |
| govulncheck | latest | the `vuln` CI job |
| Playwright + chromium | from `web/package-lock.json` | the `e2e` CI job |
| psql | 15 client | the migration loop in CI, and `docs/deployment.md` |
| Docker CLI + compose | host daemon | `make up`, `make migrate`, `make campaign` |
| Terraform, Helm, kubectl | current | `deploy/terraform`, `deploy/helm` |
| gh, jq, make | current | release and runbook workflows |

A local `make lint` that disagrees with CI is worse than no local lint,
so when you bump a version in `.github/workflows/ci.yml`, bump the
matching `ARG` in `.devcontainer/Dockerfile` in the same change.

## Networking

The container runs with `--network=host`. Every `localhost` in this
repository therefore keeps its meaning inside it: `MIGRATE_DSN` and
`.env.example` point at `localhost:5432`, and `scripts/e2e.sh` polls
`127.0.0.1:18080`. Ports published by `docker compose up -d db` land on
the same loopback the container sees, so `forwardPorts` is neither
needed nor effective.

Docker itself is the **host's** daemon, mounted in rather than nested.
`docker compose ps` inside the container shows the same `arbd-paper` and
`db` as a terminal outside it — which is the point, but also means
`docker compose down` in here stops the real stack.

Host networking is a Linux capability. On macOS or Windows the container
still builds, but `localhost` no longer bridges to the host, so the
compose-backed targets need the DSNs repointing at `host.docker.internal`.

## Git over SSH

`origin` is `git@github.com:cploutarchou/arb-chain-bot.git`, so pushing
needs a key. The container **forwards the host's SSH agent** rather than
copying keys in: `devcontainer.json` binds `$SSH_AUTH_SOCK` to
`/ssh-agent` and repoints `SSH_AUTH_SOCK` at it. Your private key never
enters the container filesystem or any image layer.

The precondition is an agent running on the host with a key loaded:

```bash
ssh-add -l                       # should list at least one key
ssh-add ~/.ssh/id_ed25519        # if it does not
```

GNOME Keyring and most desktop sessions already provide one. If
`SSH_AUTH_SOCK` is unset when the container is created, the bind mount
has no source and creation fails — start an agent, then rebuild.

`post-start.sh` reports the state on every start. To check by hand:

```bash
git ls-remote origin >/dev/null && echo ok
```

Use that rather than `ssh -T git@github.com`, which exits non-zero even
when authentication succeeded.

Two things worth knowing:

- With a keyring-backed agent, the first push may appear to hang while
  the passphrase prompt waits **on the host desktop**, not in the
  container. It is not a broken mount.
- `~/.gitconfig` is mounted read-only, so commits are authored with your
  host identity and there is no second place to keep it in step.

## First run

`post-create.sh` fetches Go modules, runs `npm ci` in both `web/` and
`site/`, seeds `known_hosts` for github.com, and downloads the Playwright
browsers into `/opt/pw-browsers`. The browser download is the slowest
step and the only non-fatal one: if it fails, the Go and Next.js suites
still run and you can retry with

```bash
npx --prefix web playwright install --with-deps chromium
```

Then the usual loop:

```bash
make up && make migrate      # Postgres + schema
make all                     # gofmt, vet, tests, build
./scripts/e2e.sh             # Playwright against a real arbd
```

## The storage integration tests are destructive

`internal/storage`'s tests `DELETE` from nearly every table in whatever
`ARB_TEST_DATABASE_URL` names — settings, screener rules, paper
positions and executions, vault secrets and users included. Pointing
that variable at a working database destroys it. This has happened once
already, to the `arb` development database, which is why the helper now
refuses to run without an explicit opt-in.

Use a disposable database:

```bash
make test-db                 # drops and recreates arb_test, applies migrations
ARB_TEST_DATABASE_URL="postgres://arb:${POSTGRES_PASSWORD}@localhost:5432/arb_test?sslmode=disable" \
ARB_TEST_DB_DESTRUCTIVE=1 go test -race ./internal/storage/
```

`make all` and a plain `go test -race ./...` are safe: without
`ARB_TEST_DATABASE_URL` the package skips. CI is safe too — its Postgres
is a per-job service container, thrown away with the job.

## VS Code workspace settings

`.vscode/` is committed (`settings.json`, `extensions.json`,
`launch.json`, `tasks.json`); everything else under it stays ignored.
The settings format Go with `gofmt` rather than goimports so a clean
save is a clean `gofmt -l cmd internal` in CI, and point ESLint at both
`web/` and `site/`, which are separate Next apps.

`launch.json` debugs `arbd` in PAPER or MARKET_DATA mode on `:18080`,
clear of the compose stack's `:8080`. `tasks.json` wraps the Makefile;
**check: everything CI runs** is the whole pre-push gate in CI's order.
