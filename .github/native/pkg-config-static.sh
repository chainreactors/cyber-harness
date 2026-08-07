#!/usr/bin/env bash
set -euo pipefail
exec pkg-config --static "$@"
