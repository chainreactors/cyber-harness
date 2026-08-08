#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

# Calling usr/bin/bash directly does not enter the MINGW64 environment. Make
# the toolchain selection explicit so local builds and GitHub Actions agree.
export MSYSTEM=MINGW64
export PATH="/mingw64/bin:/usr/bin:${PATH}"
if ! gcc -dumpmachine | grep -q 'mingw32$'; then
  echo "a MinGW-w64 GCC toolchain is required" >&2
  exit 1
fi

ARCH="${GOARCH:-}"
if [[ -z "${ARCH}" ]] && command -v go >/dev/null 2>&1; then
  ARCH="$(go env GOARCH)"
fi
if [[ -z "${ARCH}" ]]; then
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    *) echo "cannot determine recorder target architecture" >&2; exit 1 ;;
  esac
fi
if [[ "${ARCH}" != "amd64" ]]; then
  echo "record native Windows build only supports amd64" >&2
  exit 1
fi

PREFIX="${AISCAN_RECORD_PREFIX:-${ROOT}/.cache/record-native/windows-${ARCH}}"
SOURCE_ROOT="${AISCAN_RECORD_SOURCE:-${ROOT}/.cache/record-native/src}"
STAMP="${PREFIX}/.versions"
EXPECTED="source_bundle=${RECORD_NATIVE_VERSION} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"

emit_env() {
  if [[ -n "${GITHUB_ENV:-}" ]]; then
    PREFIX_WIN="$(cygpath -w "${PREFIX}")"
    ROOT_WIN="$(cygpath -w "${ROOT}")"
    echo "PKG_CONFIG_PATH=${PREFIX_WIN}\\lib\\pkgconfig" >> "${GITHUB_ENV}"
    echo "PKG_CONFIG=${ROOT_WIN}\\.github\\native\\pkg-config-static.cmd" >> "${GITHUB_ENV}"
    echo "CGO_CFLAGS=-I${PREFIX_WIN}\\include" >> "${GITHUB_ENV}"
    echo "CGO_LDFLAGS=-L${PREFIX_WIN}\\lib -static -static-libgcc" >> "${GITHUB_ENV}"
  fi
}

if [[ -f "${STAMP}" ]] && [[ "$(cat "${STAMP}")" == "${EXPECTED}" ]]; then
  echo "record native dependencies already built at ${PREFIX}"
  emit_env
  exit 0
fi

mkdir -p "${SOURCE_ROOT}" "${PREFIX}"

if [[ ! -d "${SOURCE_ROOT}/x264/.git" ]]; then
  git clone "${X264_REPOSITORY}" "${SOURCE_ROOT}/x264"
fi
git -C "${SOURCE_ROOT}/x264" fetch --depth 1 origin "${X264_COMMIT}"
git -C "${SOURCE_ROOT}/x264" checkout --detach "${X264_COMMIT}"
(
  cd "${SOURCE_ROOT}/x264"
  make distclean >/dev/null 2>&1 || true
  ./configure \
    --prefix="${PREFIX}" \
    --host=x86_64-w64-mingw32 \
    --enable-static --disable-cli \
    --bit-depth=8 --chroma-format=420 \
    --disable-opencl --disable-interlaced
  make -j"$(nproc)"
  make install
)

if [[ ! -d "${SOURCE_ROOT}/ffmpeg/.git" ]]; then
  git clone "${FFMPEG_REPOSITORY}" "${SOURCE_ROOT}/ffmpeg"
fi
git -C "${SOURCE_ROOT}/ffmpeg" fetch --depth 1 origin "${FFMPEG_COMMIT}"
git -C "${SOURCE_ROOT}/ffmpeg" checkout --detach "${FFMPEG_COMMIT}"
(
  cd "${SOURCE_ROOT}/ffmpeg"
  make distclean >/dev/null 2>&1 || true
  PKG_CONFIG_PATH="${PREFIX}/lib/pkgconfig" ./configure \
    --prefix="${PREFIX}" \
    --disable-shared --enable-static \
    --disable-programs --disable-doc --disable-debug --disable-network \
    --disable-autodetect --disable-everything \
    --enable-gpl --enable-libx264 \
    --enable-indev=gdigrab --enable-encoder=libx264 \
    --enable-muxer=mp4 --enable-protocol=file --enable-swscale \
    --extra-cflags="-I${PREFIX}/include" \
    --extra-ldflags="-L${PREFIX}/lib"
  bash "${ROOT}/.github/native/verify-ffmpeg-config.sh" windows config_components.h
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
