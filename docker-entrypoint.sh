#!/bin/sh
# The data directory is a bind mount, so its owner is whatever the host used.
# Start as root just long enough to make it writable for the runtime user, then
# drop privileges. An explicit user (or an already-writable mount) skips this.
set -e

app_user="${PUID:-10001}"
app_group="${PGID:-10001}"

if [ "$(id -u)" = "0" ]; then
	if ! chown -R "$app_user:$app_group" "${DATA_DIR:-/data}" 2>/dev/null; then
		echo "warning: could not change ownership of ${DATA_DIR:-/data}; continuing" >&2
	fi
	exec su-exec "$app_user:$app_group" "$@"
fi

exec "$@"