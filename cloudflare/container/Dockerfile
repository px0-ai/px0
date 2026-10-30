# px0-cf container image: a Go supervisor (pxd) that spawns the real,
# unmodified px0 binary (fetched via upstream's own install.sh) once per
# resident repo, sharing one container across many repos with LRU eviction
# under disk/memory pressure. See cloudflare/container/pxd for the
# supervisor source.
#
# px0 itself is deliberately NOT built from this fork's vendored Go source
# — pulling in upstream improvements means bumping to a newer px0 release,
# which this image picks up automatically on every rebuild since
# install.sh defaults to VERSION=latest.

FROM golang:1.23-alpine AS pxd-build
WORKDIR /src
COPY pxd/go.mod ./
COPY pxd/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pxd .

FROM alpine:3.20

RUN apk add --no-cache git curl ca-certificates

# Installs a released px0 binary for linux/amd64 to /usr/local/bin/px0,
# exactly as any user would via `curl -fsSL https://px0.ai/install.sh | sh`.
RUN curl -fsSL https://raw.githubusercontent.com/px0-ai/px0/master/install.sh | sh \
    && px0 -version

COPY --from=pxd-build /pxd /usr/local/bin/pxd

# 7777: reverse proxy to whichever resident repo's px0 instance a request
# is for. 8081: per-repo status API the DO polls for clone readiness.
EXPOSE 7777 8081

ENTRYPOINT ["/usr/local/bin/pxd"]
