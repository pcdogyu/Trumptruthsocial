#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_FILE="$ROOT_DIR/upgrade.log"

exec >>"$LOG_FILE" 2>&1

export PATH="/usr/local/go/bin:/usr/bin:/bin:/usr/sbin:/sbin:${PATH:-}"

echo "[$(date -Iseconds)] upgrade started"
cd "$ROOT_DIR"

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
if [[ "$CURRENT_BRANCH" != "golang" ]]; then
  echo "[$(date -Iseconds)] expected golang branch, got $CURRENT_BRANCH"
  exit 1
fi

echo "[$(date -Iseconds)] pulling latest code"
git pull --ff-only origin golang

echo "[$(date -Iseconds)] building truthsocial.exe"
go build -o truthsocial.exe .

echo "[$(date -Iseconds)] restarting truthsocial.service"
systemctl restart truthsocial.service

echo "[$(date -Iseconds)] upgrade finished"
