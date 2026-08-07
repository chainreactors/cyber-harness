#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

ARCH="$(go env GOARCH)"
PREFIX="${AISCAN_RECORD_PREFIX:-${ROOT}/.cache/record-native/linux-${ARCH}}"
SOURCE_ROOT="${AISCAN_RECORD_SOURCE:-${ROOT}/.cache/record-native/src}"
STAMP="${PREFIX}/.versions"
EXPECTED="source_bundle=${RECORD_NATIVE_VERSION} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"

emit_env() {
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    echo "PKG_CONFIG_PATH=${PREFIX}/lib/pkgconfig" >> "${GITHUB_ENV}"
    echo "PKG_CONFIG=${ROOT}/.github/native/pkg-config-static.sh" >> "${GITHUB_ENV}"
    echo "CGO_CFLAGS=-I${PREFIX}/include" >> "${GITHUB_ENV}"
    echo "CGO_LDFLAGS=-L${PREFIX}/lib" >> "${GITHUB_ENV}"
  fi
}

if [[ -f "${STAMP}" ]] && [[ "$(cat "${STAMP}")" == "${EXPECTED}" ]]; then
  echo "record native dependencies already built at ${PREFIX}"
  emit_env
  exit 0
fi

mkdir -p "${SOURCE_ROOT}" "${PREFIX}"

if [[ ! -d "${SOURCE_ROOT}/x264/.git" ]]; then
  git clone https://code.videolan.org/videolan/x264.git "${SOURCE_ROOT}/x264"
fi
git -C "${SOURCE_ROOT}/x264" fetch --depth 1 origin "${X264_COMMIT}"
git -C "${SOURCE_ROOT}/x264" checkout --detach "${X264_COMMIT}"
(
  cd "${SOURCE_ROOT}/x264"
  make distclean >/dev/null 2>&1 || true
  ./configure \
    --prefix="${PREFIX}" \
    --enable-static --enable-pic --disable-cli \
    --bit-depth=8 --chroma-format=420 \
    --disable-opencl --disable-interlaced
  make -j"$(nproc)"
  make install
)

if [[ ! -d "${SOURCE_ROOT}/ffmpeg/.git" ]]; then
  git clone https://github.com/FFmpeg/FFmpeg.git "${SOURCE_ROOT}/ffmpeg"
fi
git -C "${SOURCE_ROOT}/ffmpeg" fetch --depth 1 origin "${FFMPEG_COMMIT}"
git -C "${SOURCE_ROOT}/ffmpeg" checkout --detach "${FFMPEG_COMMIT}"
(
  cd "${SOURCE_ROOT}/ffmpeg"
  make distclean >/dev/null 2>&1 || true
  PKG_CONFIG_PATH="${PREFIX}/lib/pkgconfig" ./configure \
    --prefix="${PREFIX}" \
    --disable-shared --enable-static --enable-pic \
    --disable-programs --disable-doc --disable-debug --disable-network \
    --disable-autodetect --disable-everything \
    --enable-gpl --enable-libx264 \
    --enable-indev=xcbgrab --enable-decoder=rawvideo \
    --enable-encoder=libx264 --enable-muxer=mp4 \
    --enable-protocol=file --enable-swscale \
    --enable-libxcb --enable-libxcb-shm --enable-libxcb-shape --enable-libxcb-xfixes \
    --extra-cflags="-I${PREFIX}/include" \
    --extra-ldflags="-L${PREFIX}/lib"
  bash "${ROOT}/.github/native/verify-ffmpeg-config.sh" linux config_components.h
  make -j"$(nproc)"
  make install
)

mkdir -p "${PREFIX}/share/licenses/ffmpeg" "${PREFIX}/share/licenses/x264"
for license in COPYING.GPLv2 COPYING.GPLv3 LICENSE.md; do
  if [[ -f "${SOURCE_ROOT}/ffmpeg/${license}" ]]; then
    cp "${SOURCE_ROOT}/ffmpeg/${license}" "${PREFIX}/share/licenses/ffmpeg/"
  fi
done
if [[ -f "${SOURCE_ROOT}/x264/COPYING" ]]; then
  cp "${SOURCE_ROOT}/x264/COPYING" "${PREFIX}/share/licenses/x264/"
fi

printf '%s' "${EXPECTED}" > "${STAMP}"
echo "record native dependencies built at ${PREFIX}"

emit_env
