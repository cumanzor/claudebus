#!/usr/bin/env bash
# deploy the relay to the NUC: rsync source, build with the NUC's Go, seed a
# token if missing, install + start the systemd unit. Idempotent.
set -euo pipefail

HOST="${CBUS_RELAY_HOST:-nuc}"
DEST="/home/relay/cbus-relay"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "== rsync source =="
ssh "$HOST" "mkdir -p $DEST/src"
rsync -a --delete \
  "$here/go.mod" "$here/internal" "$here/cmd" "$here/cbus-relay.service" \
  "$HOST:$DEST/src/"

echo "== build on $HOST =="
ssh "$HOST" "cd $DEST/src && go build -o $DEST/cbus-relay ./cmd/cbus-relay && go build -o $DEST/wstail ./cmd/wstail && $DEST/cbus-relay -h 2>&1 | head -1 || true"

echo "== token =="
ssh "$HOST" "[ -s $DEST/token ] && echo 'token exists' || { umask 077 && openssl rand -hex 32 > $DEST/token && echo 'token created'; }"

echo "== systemd unit =="
# enable for boot persistence, then RESTART to load the freshly-built binary.
# `enable --now` only *starts* a stopped unit — it is a no-op on an already-
# active one, so it would silently keep serving the old binary after a rebuild.
ssh "$HOST" "sudo cp $DEST/src/cbus-relay.service /etc/systemd/system/cbus-relay.service && sudo systemctl daemon-reload && sudo systemctl enable cbus-relay && sudo systemctl restart cbus-relay && sleep 1 && sudo systemctl is-active cbus-relay"

echo "== health =="
ssh "$HOST" "curl -fsS http://127.0.0.1:8090/healthz"
echo "deployed."
