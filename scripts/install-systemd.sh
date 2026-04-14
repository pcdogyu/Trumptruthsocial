#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="truthsocial.service"
UPGRADE_SERVICE_NAME="truthsocial-upgrade.service"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"
UPGRADE_SERVICE_PATH="/etc/systemd/system/${UPGRADE_SERVICE_NAME}"
APP_BIN="/opt/Trumptruthsocial/truthsocial.exe"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "Please run as root."
  exit 1
fi

if systemctl is-active --quiet "$SERVICE_NAME"; then
  systemctl stop "$SERVICE_NAME"
fi
if systemctl is-active --quiet "$UPGRADE_SERVICE_NAME"; then
  systemctl stop "$UPGRADE_SERVICE_NAME"
fi

pkill -f "$APP_BIN" || true

install -m 0644 "$ROOT_DIR/deploy/truthsocial.service" "$SERVICE_PATH"
install -m 0644 "$ROOT_DIR/deploy/truthsocial-upgrade.service" "$UPGRADE_SERVICE_PATH"
systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"
systemctl status --no-pager --full "$SERVICE_NAME"
