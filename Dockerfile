FROM node:24-alpine AS frontend

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web ./
RUN npm run build

FROM golang:1.25-alpine AS backend

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY --from=frontend /src/internal/webassets/dist ./internal/webassets/dist
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/cline-pass-switcher \
    ./cmd/cline-pass-switcher

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 10001 app \
    && adduser -D -H -u 10001 -G app app \
    && mkdir -p /data \
    && chown app:app /data

WORKDIR /app
COPY --from=backend /out/cline-pass-switcher /app/cline-pass-switcher

ENV DATA_DIR=/data
ENV BIND_HOST=0.0.0.0
ENV PORT=3123

VOLUME ["/data"]
EXPOSE 3123

USER 10001:10001

ENTRYPOINT ["/app/cline-pass-switcher"]
