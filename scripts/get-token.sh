#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

log() {
  printf '%s\n' "$*" >&2
}

ensure_browser_installed() {
  if find_browser >/dev/null 2>&1; then
    return 0
  fi

  if command -v snap >/dev/null 2>&1; then
    log "no browser found; installing Chromium snap"
    snap install chromium >/dev/null
    ensure_chromium_snap_runtime >/dev/null 2>&1 || true
    return 0
  fi

  return 1
}

ensure_chromium_snap_runtime() {
  if ! command -v snap >/dev/null 2>&1; then
    return 1
  fi

  if ! snap list mesa-2404 >/dev/null 2>&1; then
    log "installing Chromium GPU runtime snap mesa-2404"
    snap install mesa-2404 >/dev/null
  fi

  if snap list gnome-46-2404 >/dev/null 2>&1; then
    snap connect chromium:gnome-46-2404 gnome-46-2404:gnome-46-2404 >/dev/null 2>&1 || true
  fi
  if snap list mesa-2404 >/dev/null 2>&1; then
    snap connect chromium:gpu-2404 mesa-2404:gpu-2404 >/dev/null 2>&1 || true
  fi
  if snap list gtk-common-themes >/dev/null 2>&1; then
    snap connect chromium:gtk-3-themes gtk-common-themes:gtk-3-themes >/dev/null 2>&1 || true
    snap connect chromium:icon-themes gtk-common-themes:icon-themes >/dev/null 2>&1 || true
    snap connect chromium:sound-themes gtk-common-themes:sound-themes >/dev/null 2>&1 || true
  fi
}

ensure_xvfb_installed() {
  if command -v xvfb-run >/dev/null 2>&1; then
    return 0
  fi

  if command -v apt-get >/dev/null 2>&1; then
    log "xvfb-run not found; installing xvfb"
    apt-get update >/dev/null
    DEBIAN_FRONTEND=noninteractive apt-get install -y xvfb >/dev/null
    return 0
  fi

  return 1
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

if ! browser_path="$(find_browser)"; then
  if ! ensure_browser_installed; then
    log "error: no Chrome/Chromium/Edge executable found"
    log "set TRUTHSOCIAL_CHROME_PATH=/full/path/to/browser if it is installed"
    exit 1
  fi
  ensure_chromium_snap_runtime >/dev/null 2>&1 || true
  browser_path="$(find_browser)" || {
    log "error: browser installation completed but no executable was detected"
    exit 1
  }
fi

if [[ -n "$browser_path" ]]; then
  export TRUTHSOCIAL_CHROME_PATH="$browser_path"
  log "using browser: $TRUTHSOCIAL_CHROME_PATH"
fi

if [[ -z "${DISPLAY:-}" && -z "${WAYLAND_DISPLAY:-}" ]]; then
  if ! ensure_xvfb_installed; then
    log "no display detected and xvfb-run is unavailable"
    log "install xvfb or run the script from a graphical session"
    exit 1
  fi
  log "no display detected; running token login under xvfb-run"
  exec xvfb-run -a "${TOKEN_CMD[@]}" "$@"
fi

exec "${TOKEN_CMD[@]}" "$@"
