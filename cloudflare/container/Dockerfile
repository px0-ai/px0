# px0-cf container image: the real, unmodified px0 binary, fetched via
# upstream's own install.sh, plus git for cloning the repo being browsed.
#
# Deliberately NOT built from this fork's vendored Go source — pulling in
# upstream improvements means bumping to a newer px0 release, which this
# image picks up automatically on every rebuild since install.sh defaults
# to VERSION=latest.

FROM alpine:3.20

RUN apk add --no-cache git curl ca-certificates darkhttpd

# Installs a released px0 binary for linux/amd64 to /usr/local/bin/px0,
# exactly as any user would via `curl -fsSL https://px0.ai/install.sh | sh`.
RUN curl -fsSL https://raw.githubusercontent.com/px0-ai/px0/master/install.sh | sh \
    && px0 -version

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# 7777: px0 itself. 8081: tiny status server the DO polls for clone readiness.
EXPOSE 7777 8081

ENTRYPOINT ["/entrypoint.sh"]
