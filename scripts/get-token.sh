#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

log() {
  printf '%s\n' "$*" >&2
}

find_browser() {
  local override candidate

  override="${TRUTHSOCIAL_CHROME_PATH:-}"
  if [[ -n "$override" ]]; then
    if [[ -x "$override" ]]; then
      printf '%s\n' "$override"
      return 0
    fi
    return 1
  fi

  local candidates=(
    chromium
    chromium-browser
    google-chrome
    google-chrome-stable
    google-chrome-beta
    google-chrome-unstable
    chrome
    brave-browser
    brave
    vivaldi
    microsoft-edge
    msedge
    /usr/bin/chromium
    /usr/bin/chromium-browser
    /usr/bin/google-chrome
    /usr/bin/google-chrome-stable
    /usr/bin/google-chrome-beta
    /usr/bin/google-chrome-unstable
    /usr/bin/chrome
    /usr/bin/brave-browser
    /usr/bin/brave
    /usr/bin/vivaldi
    /usr/bin/microsoft-edge
    /usr/bin/msedge
    /snap/bin/chromium
    /opt/google/chrome/google-chrome
    /opt/google/chrome/chrome
    /usr/local/bin/google-chrome
    /usr/local/bin/chromium
  )

  for candidate in "${candidates[@]}"; do
    if [[ "$candidate" == *"/"* ]]; then
      if [[ -x "$candidate" ]]; then
        printf '%s\n' "$candidate"
        return 0
      fi
      continue
    fi

    if command -v "$candidate" >/dev/null 2>&1; then
      command -v "$candidate"
      return 0
    fi
  done

  return 1
}

TOKEN_CMD=()
if [[ -x ./truthsocial.exe ]]; then
  TOKEN_CMD=(./truthsocial.exe get-token)
elif command -v go >/dev/null 2>&1; then
  TOKEN_CMD=(go run . get-token)
else
  log "error: neither ./truthsocial.exe nor go is available"
  exit 1
fi

if browser_path="$(find_browser)"; then
  export TRUTHSOCIAL_CHROME_PATH="$browser_path"
  log "using browser: $TRUTHSOCIAL_CHROME_PATH"
else
  log "error: no Chrome/Chromium/Edge executable found"
  log "set TRUTHSOCIAL_CHROME_PATH=/full/path/to/browser if it is installed"
  exit 1
fi

if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" && "$(command -v xvfb-run || true)" != "" ]]; then
  log "no display detected; running token login under xvfb-run"
  exec xvfb-run -a "${TOKEN_CMD[@]}" "$@"
fi

exec "${TOKEN_CMD[@]}" "$@"
