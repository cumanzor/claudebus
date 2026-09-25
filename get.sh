#!/bin/sh
# cbus bootstrap installer for macOS and Linux.
#
# Downloads the cbus binary from a GitHub release (with gh when it is installed and
# authenticated, otherwise anonymously with curl), checks it against the release's
# SHA256SUMS, then installs the /bus-* skill commands and role prompts it carries. After the first
# install, update in place with `cbus selfupdate` — no need to re-run this.
#
# The repo slug is NOT baked into this script (it stays out of committed source so
# the repo can change visibility without a history rewrite); pass it in:
#
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo sh
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo CBUS_INSTALL_DIR=/usr/local/bin sh
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo CBUS_VERSION=v0.1.0 sh
#
# NOTE: the legacy install.sh (bash restore) and install-cbus-go.sh are retired
# from the tree; recover them from git history if ever needed.

set -eu

REPO="${CBUS_REPO:-}"
INSTALL_DIR="${CBUS_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${CBUS_VERSION:-latest}"

if [ -z "$REPO" ]; then
    echo "cbus: set CBUS_REPO=owner/repo (the release repo is not baked into this script)" >&2
    exit 1
fi

case "$(uname -s)" in
    Darwin) OS=darwin ;;
    Linux)  OS=linux ;;
    *) echo "cbus: unsupported OS $(uname -s) (cbus ships darwin and linux only)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
    x86_64|amd64)  ARCH=amd64 ;;
    arm64|aarch64) ARCH=arm64 ;;
    *) echo "cbus: unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
BIN="cbus-${OS}-${ARCH}"

BASE="${CBUS_RELEASE_BASE_URL:-https://github.com}"
BASE="${BASE%/}"
# a userinfo part would let "http://localhost:x@host" pass the loopback check below
case "$BASE" in
    *@*) echo "cbus: CBUS_RELEASE_BASE_URL must not contain credentials or '@'" >&2; exit 1 ;;
esac
case "$BASE" in
    https://*) ;;
    http://127.0.0.1|http://127.0.0.1:*|http://127.0.0.1/*) ;;
    http://localhost|http://localhost:*|http://localhost/*) ;;
    "http://[::1]"|"http://[::1]:"*|"http://[::1]/"*) ;;
    *) echo "cbus: CBUS_RELEASE_BASE_URL must be https:// (plain http only for 127.0.0.1, localhost or [::1])" >&2; exit 1 ;;
esac
if command -v sha256sum >/dev/null 2>&1; then
    SHA="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
    SHA="shasum -a 256"
else
    echo "cbus: need sha256sum or shasum to verify the download" >&2
    exit 1
fi
# gh is optional: it is used when installed and authenticated; otherwise curl reads
# the public release anonymously. A gh failure is reported, never retried with curl.
# with an https base, a redirect may never downgrade to plain http
CURL_PROTO=""
case "$BASE" in https://*) CURL_PROTO="--proto-redir =https" ;; esac
USE_GH=0
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
    USE_GH=1
elif ! command -v curl >/dev/null 2>&1; then
    echo "cbus: need curl (or an authenticated gh) to download the release" >&2
    exit 1
fi

mkdir -p "$INSTALL_DIR"
OUT="$INSTALL_DIR/cbus"
# download into a temp dir on the SAME filesystem, verify the checksum and that it
# runs, then atomically move it over $OUT: a refused or interrupted install must
# never damage a working one (selfupdate's own order; C7).
# rel names the release in messages: "release vX.Y.Z", or "the latest release" when gh
# fetched latest without resolving a tag first
rel() { if [ "$TAG" = latest ]; then echo "the latest release"; else echo "release $TAG"; fi; }
TMPD="$INSTALL_DIR/.cbus-install.$$"
TMP="$TMPD/$BIN"
trap 'rm -rf "$TMPD"' EXIT
mkdir -p "$TMPD"

if [ "$USE_GH" = 1 ]; then
    TAG="$VERSION"
    echo "cbus: downloading $VERSION $BIN with gh..."
    if [ "$VERSION" = "latest" ]; then
        gh release download --repo "$REPO" --pattern "$BIN" --pattern SHA256SUMS --dir "$TMPD"
    else
        gh release download "$VERSION" --repo "$REPO" --pattern "$BIN" --pattern SHA256SUMS --dir "$TMPD"
    fi
else
    if [ "$VERSION" = "latest" ]; then
        # shellcheck disable=SC2086
        LOC=$(curl -fsS $CURL_PROTO -o /dev/null -w '%{redirect_url}' "$BASE/$REPO/releases/latest") || {
            echo "cbus: could not resolve the latest release of $REPO" >&2; exit 1; }
        TAG="${LOC##*/releases/tag/}"
        case "$TAG" in ""|*/*) echo "cbus: could not resolve the latest release of $REPO (got '$LOC')" >&2; exit 1 ;; esac
    else
        TAG="$VERSION"
    fi
    echo "cbus: downloading $TAG $BIN..."
    # shellcheck disable=SC2086
    curl -fsSL $CURL_PROTO -o "$TMPD/SHA256SUMS" "$BASE/$REPO/releases/download/$TAG/SHA256SUMS" || {
        echo "cbus: $(rel) has no SHA256SUMS: it carries no verifiable binaries, refusing to install" >&2; exit 1; }
    # shellcheck disable=SC2086
    curl -fsSL $CURL_PROTO -o "$TMP" "$BASE/$REPO/releases/download/$TAG/$BIN" || {
        echo "cbus: $(rel) has no $BIN asset" >&2; exit 1; }
fi
[ -s "$TMPD/SHA256SUMS" ] || {
    echo "cbus: $(rel) has no SHA256SUMS: it carries no verifiable binaries, refusing to install" >&2; exit 1; }
[ -s "$TMP" ] || { echo "cbus: $(rel) has no $BIN asset" >&2; exit 1; }
WANT=$(awk -v n="$BIN" 'NF == 2 && ($2 == n || $2 == "*" n) { print tolower($1); exit }' "$TMPD/SHA256SUMS")
[ -n "$WANT" ] || {
    echo "cbus: SHA256SUMS of $(rel) has no line for $BIN, refusing to install a binary it cannot verify" >&2; exit 1; }
GOT=$($SHA "$TMP" | awk '{ print tolower($1) }')
[ "$GOT" = "$WANT" ] || {
    echo "cbus: checksum mismatch for $BIN in $(rel): SHA256SUMS says $WANT, download is $GOT, refusing to install it" >&2; exit 1; }
chmod +x "$TMP"

echo ""
"$TMP" --version          # the download must run before it replaces anything
mv -f "$TMP" "$OUT"       # same-fs atomic rename
echo "installed: $OUT"

# install the skill commands and role prompts the binary carries.
"$OUT" install-commands --force || echo "cbus: note: install-commands reported problems (see above)" >&2
"$OUT" install-roles --force || echo "cbus: note: install-roles reported problems (see above)" >&2
"$OUT" install-codex-skills || echo "cbus: note: install-codex-skills reported problems (see above)" >&2

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "cbus: note: $INSTALL_DIR is not on \$PATH — add it to your shell init" >&2 ;;
esac

echo ""
echo "cbus is installed. keep it current with: cbus selfupdate"
