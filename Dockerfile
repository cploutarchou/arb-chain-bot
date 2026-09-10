# Multi-stage build for the arbd platform binary and the §80 campaign
# tool. The go.mod toolchain directive pins the exact Go patch release;
# GOTOOLCHAIN=auto (the default) fetches it inside the builder.
FROM golang:1.25@sha256:699337d620559a59b4a2bb298ad59611e535d2ee755a34cf2d2a98f37578dc80 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/arbd ./cmd/arbd \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/campaign ./cmd/campaign

# Runtime: TLS roots for the exchange connection, an unprivileged user,
# and a writable /recordings volume for RECORD mode.
FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
RUN apk add --no-cache ca-certificates wget \
 && addgroup -S arb && adduser -S -G arb arb \
 && mkdir -p /recordings && chown arb:arb /recordings
COPY --from=build /out/arbd /usr/local/bin/arbd
COPY --from=build /out/campaign /usr/local/bin/campaign
USER arb
VOLUME /recordings
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s \
  CMD wget -qO- http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["arbd"]
