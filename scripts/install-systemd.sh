#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SERVICE_NAME="truthsocial.service"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "Please run as root."
  exit 1
fi

install -m 0644 "$ROOT_DIR/deploy/truthsocial.service" "$SERVICE_PATH"
systemctl daemon-reload
systemctl enable --now "$SERVICE_NAME"
systemctl status --no-pager --full "$SERVICE_NAME"
