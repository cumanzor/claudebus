#!/usr/bin/env bash
# deploy the relay to the NUC: rsync source, build with the NUC's Go, seed a
# token if missing, install + start the systemd unit. Idempotent.
set -euo pipefail

HOST="${CBUS_RELAY_HOST:-nuc}"
DEST="/home/relay/cbus-relay"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
root="$(cd "$here/.." && pwd)" # repo root: go.mod (module claudebus) + internal/core live here

echo "== rsync source =="
ssh "$HOST" "mkdir -p $DEST/src"
# ship the whole module so it stays self-contained on the NUC: root go.mod,
# the shared internal/ (core), and the relay tree (cmd + internal + service).
rsync -a --delete \
  "$root/go.mod" "$root/internal" "$root/relay" \
  "$HOST:$DEST/src/"

# migration cleanup: the pre-restructure layout kept cmd/ and cbus-relay.service
# at $DEST/src/ top level. `rsync --delete` only prunes WITHIN the dirs it
# transfers (internal/, relay/), never the parent, so those two would linger —
# and the stale cmd/ still compiles under the new module, muddying forensics.
# Remove them explicitly. Idempotent; safe to delete once every node is rebuilt.
ssh "$HOST" "rm -rf ${DEST:?}/src/cmd ${DEST:?}/src/cbus-relay.service"

echo "== build on $HOST =="
ssh "$HOST" "cd $DEST/src && go build -o $DEST/cbus-relay ./relay/cmd/cbus-relay && go build -o $DEST/wstail ./relay/cmd/wstail && $DEST/cbus-relay -h 2>&1 | head -1 || true"

echo "== token =="
ssh "$HOST" "[ -s $DEST/token ] && echo 'token exists' || { umask 077 && openssl rand -hex 32 > $DEST/token && echo 'token created'; }"

echo "== systemd unit =="
# enable for boot persistence, then RESTART to load the freshly-built binary.
# `enable --now` only *starts* a stopped unit — it is a no-op on an already-
# active one, so it would silently keep serving the old binary after a rebuild.
ssh "$HOST" "sudo cp $DEST/src/relay/cbus-relay.service /etc/systemd/system/cbus-relay.service && sudo systemctl daemon-reload && sudo systemctl enable cbus-relay && sudo systemctl restart cbus-relay && sleep 1 && sudo systemctl is-active cbus-relay"

echo "== health =="
ssh "$HOST" "curl -fsS http://127.0.0.1:8090/healthz"
echo "deployed."
