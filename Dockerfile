FROM node:24.22.1-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS frontend

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.26.10-alpine@sha256:a2012b4a1d1f9f7e9f44e0d39b805a901e856b1dd80b4e6ee4338db547050308 AS backend

ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=frontend /src/internal/webassets/dist ./internal/webassets/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X github.com/Hxjcc/cline-pass-switcher-go/internal/buildinfo.Version=${VERSION}" \
    -o /out/cline-pass-switcher \
    ./cmd/cline-pass-switcher

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

# Require the trust-store update for the compromised eMudhra certificates.
RUN apk add --no-cache \
    'ca-certificates>=20261020-r0' 'ca-certificates-bundle>=20261020-r0' \
    tzdata su-exec \
    && addgroup -g 10001 app \
    && adduser -D -H -u 10001 -G app app \
    && mkdir -m 700 -p /data

WORKDIR /app
COPY --from=backend /out/cline-pass-switcher /app/cline-pass-switcher
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod 755 /usr/local/bin/docker-entrypoint.sh

ENV DATA_DIR=/data
ENV BIND_HOST=0.0.0.0
ENV PORT=3123

VOLUME ["/data"]
EXPOSE 3123

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null "http://127.0.0.1:${PORT}/healthz" || exit 1

# The entrypoint makes /data writable for the runtime user (PUID/PGID, default
# 10001) and then drops privileges, so the server process itself never runs as
# root and the bind mount needs no manual chown.
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/app/cline-pass-switcher"]
