#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="$ROOT_DIR/upgrade.log"

exec > >(tee -a "$LOG_FILE") 2>&1

export PATH="/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"
export HOME="${HOME:-/root}"
export GOPATH="${GOPATH:-$HOME/go}"
export GOMODCACHE="${GOMODCACHE:-$GOPATH/pkg/mod}"
export GOCACHE="${GOCACHE:-$ROOT_DIR/.gocache}"

mkdir -p "$GOPATH" "$GOMODCACHE" "$GOCACHE"

echo "[$(date -Iseconds)] upgrade started"
cd "$ROOT_DIR"

trap 'status=$?; echo "[$(date -Iseconds)] upgrade failed with exit code ${status}"; exit "${status}"' ERR

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$CURRENT_BRANCH" != "golang" ]]; then
  echo "[$(date -Iseconds)] expected golang branch, got $CURRENT_BRANCH"
  exit 1
fi

echo "[$(date -Iseconds)] pulling latest code"
git pull --ff-only origin golang

echo "[$(date -Iseconds)] building truthsocial.exe"
TMP_BIN="$ROOT_DIR/truthsocial.exe.new"
rm -f "$TMP_BIN"
go build -o "$TMP_BIN" .
mv -f "$TMP_BIN" truthsocial.exe
if [[ ! -x truthsocial.exe ]]; then
  echo "[$(date -Iseconds)] built binary missing or not executable"
  exit 1
fi
echo "[$(date -Iseconds)] binary updated: $(stat -c '%n size=%s bytes mtime=%y' truthsocial.exe)"

echo "[$(date -Iseconds)] restarting truthsocial.service"
systemctl restart truthsocial.service

echo "[$(date -Iseconds)] upgrade finished"
