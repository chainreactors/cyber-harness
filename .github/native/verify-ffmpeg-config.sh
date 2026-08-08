#!/usr/bin/env bash
set -euo pipefail

PLATFORM="${1:?usage: verify-ffmpeg-config.sh <linux|windows> <config-components.h>}"
CONFIG="${2:?usage: verify-ffmpeg-config.sh <linux|windows> <config-components.h>}"

if [[ ! -f "${CONFIG}" ]]; then
  echo "FFmpeg component config not found: ${CONFIG}" >&2
  exit 1
fi

enabled_components() {
  local kind="$1"
  sed -nE "s/^#define CONFIG_([A-Z0-9_]+)_${kind} 1$/\\1/p" "${CONFIG}" \
    | LC_ALL=C sort \
    | paste -sd, -
}

expect_components() {
  local kind="$1"
  local expected="$2"
  local actual
  actual="$(enabled_components "${kind}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "unexpected enabled FFmpeg ${kind,,} components" >&2
    echo "expected: ${expected:-<none>}" >&2
    echo "actual:   ${actual:-<none>}" >&2
    exit 1
  fi
}

case "${PLATFORM}" in
  windows)
    expect_components DECODER BMP
    expect_components INDEV GDIGRAB
    ;;
  linux)
    expect_components DECODER RAWVIDEO
    expect_components INDEV XCBGRAB
    ;;
  *)
    echo "unsupported recorder platform ${PLATFORM}" >&2
    exit 1
    ;;
esac

expect_components ENCODER LIBX264
expect_components MUXER MOV,MP4
expect_components DEMUXER ""
expect_components PROTOCOL FILE
expect_components FILTER ""
expect_components OUTDEV ""
expect_components PARSER AC3
expect_components BSF AAC_ADTSTOASC,VP9_SUPERFRAME

echo "verified minimal FFmpeg component set for ${PLATFORM}"
