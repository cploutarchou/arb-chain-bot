# Development image for arb-chain-bot. The base carries the Go toolchain
# and the standard Go language-server tooling (gopls, dlv); everything
# below is either pinned to the version CI uses or needed by a Makefile
# target. Node, Docker, gh, Terraform and Helm arrive as devcontainer
# features (devcontainer.json) rather than apt, so their versions stay
# visible in one place.
#
# go.mod pins `toolchain go1.25.14`; GOTOOLCHAIN=auto (the default)
# fetches that exact patch release on first build, so the base tag only
# needs to be the right minor.
FROM mcr.microsoft.com/devcontainers/go:1.25-bookworm

# Keep these in step with .github/workflows/ci.yml. A local `make lint`
# that disagrees with CI is worse than no local lint at all.
ARG GOLANGCI_LINT_VERSION=v2.5.0
ARG GITLEAKS_VERSION=8.30.1

# postgresql-client: `psql`, used by the migration loop CI runs and by
#   docs/deployment.md. bookworm ships the 15 client, which speaks to
#   the compose stack's postgres:16 without issue.
# jq: campaign/report JSON in scripts and runbooks.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      postgresql-client \
      jq \
      make \
      unzip \
      ca-certificates \
 && rm -rf /var/lib/apt/lists/*

# golangci-lint: pinned to the CI version and placed in /usr/local/bin.
# The base image ships its own build under $GOPATH/bin, which precedes
# /usr/local/bin on PATH, so that one is removed rather than shadowed.
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    curl -fsSL -o /tmp/gcl.tar.gz \
      "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION#v}-linux-${arch}.tar.gz"; \
    tar -xzf /tmp/gcl.tar.gz -C /tmp; \
    install -m 0755 "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION#v}-linux-${arch}/golangci-lint" /usr/local/bin/golangci-lint; \
    rm -rf /tmp/gcl.tar.gz "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION#v}-linux-${arch}"; \
    rm -f /go/bin/golangci-lint; \
    golangci-lint --version

# gitleaks: the same scanner the `secrets` CI job runs, so a leak can be
# caught before the push rather than after.
RUN set -eux; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in amd64) gl_arch=x64 ;; arm64) gl_arch=arm64 ;; *) echo "unsupported arch $arch" >&2; exit 1 ;; esac; \
    curl -fsSL -o /tmp/gitleaks.tar.gz \
      "https://github.com/gitleaks/gitleaks/releases/download/v${GITLEAKS_VERSION}/gitleaks_${GITLEAKS_VERSION}_linux_${gl_arch}.tar.gz"; \
    tar -xzf /tmp/gitleaks.tar.gz -C /tmp gitleaks; \
    install -m 0755 /tmp/gitleaks /usr/local/bin/gitleaks; \
    rm -f /tmp/gitleaks.tar.gz /tmp/gitleaks; \
    gitleaks version

# govulncheck: the `vuln` CI job. Installed into the shared GOPATH so it
# is on PATH for every user in the container.
#
# Installing as root leaves /go/pkg owned by root, and the first
# `go mod download` as the vscode user then dies on "permission denied".
# The build cache it populated is worthless to us anyway, so it is
# dropped and the base image's vscode:golang ownership restored.
RUN GOFLAGS=-mod=mod go install golang.org/x/vuln/cmd/govulncheck@latest \
 && govulncheck -version >/dev/null \
 && go clean -modcache \
 && mkdir -p /go/pkg/mod \
 && chown -R vscode:golang /go/pkg \
 && chmod -R g+rwXs /go/pkg

# Playwright downloads its browsers into $HOME by default, which a
# rebuilt container throws away. A shared, writable location survives.
ENV PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers
RUN mkdir -p /opt/pw-browsers && chown vscode:vscode /opt/pw-browsers
