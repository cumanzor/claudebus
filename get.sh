#!/bin/sh
# cbus bootstrap installer for macOS and Linux.
#
# Downloads the cbus binary from a private GitHub release via the gh CLI, then
# installs the /bus-* skill commands and role prompts it carries. After the first
# install, update in place with `cbus selfupdate` — no need to re-run this.
#
# The repo slug is NOT baked into this script (it stays out of committed source so
# the repo can change visibility without a history rewrite); pass it in:
#
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo sh
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo CBUS_INSTALL_DIR=/usr/local/bin sh
#   curl -fsSL .../get.sh | CBUS_REPO=owner/repo CBUS_VERSION=v0.1.0 sh
#
# NOTE: distinct from install.sh (the retired bash client / rollback) and
# install-cbus-go.sh (the transitional side-by-side installer) — neither is touched.

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

if ! command -v gh >/dev/null 2>&1; then
    cat >&2 <<EOF
cbus: the gh CLI is required (the repo is private).
  macOS:  brew install gh
  Linux:  https://github.com/cli/cli/blob/trunk/docs/install_linux.md
then: gh auth login   and re-run this installer.
EOF
    exit 1
fi
if ! gh auth status >/dev/null 2>&1; then
    echo "cbus: gh is installed but not authenticated — run: gh auth login" >&2
    exit 1
fi

mkdir -p "$INSTALL_DIR"
OUT="$INSTALL_DIR/cbus"
# download to a sibling temp on the SAME filesystem, verify it runs, then atomically
# move it over $OUT — a re-run that dies mid-download or fetches a short asset must
# never damage a working install (selfupdate's own order; C7).
TMP="$OUT.tmp.$$"
trap 'rm -f "$TMP"' EXIT

if [ "$VERSION" = "latest" ]; then
    echo "cbus: downloading latest $BIN..."
    gh release download --repo "$REPO" --pattern "$BIN" --output "$TMP" --clobber
else
    echo "cbus: downloading $VERSION $BIN..."
    gh release download "$VERSION" --repo "$REPO" --pattern "$BIN" --output "$TMP" --clobber
fi
chmod +x "$TMP"

echo ""
"$TMP" --version          # the download must run before it replaces anything
mv -f "$TMP" "$OUT"       # same-fs atomic rename
echo "installed: $OUT"

# install the skill commands and role prompts the binary carries.
"$OUT" install-commands --force || echo "cbus: note: install-commands reported problems (see above)" >&2
"$OUT" install-roles --force || echo "cbus: note: install-roles reported problems (see above)" >&2

case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "cbus: note: $INSTALL_DIR is not on \$PATH — add it to your shell init" >&2 ;;
esac

echo ""
echo "cbus is installed. keep it current with: cbus selfupdate"
