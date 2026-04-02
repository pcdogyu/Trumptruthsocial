#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

if [[ ! -x "./truthsocial" ]]; then
  go build -o truthsocial .
fi

exec ./truthsocial "$@"
