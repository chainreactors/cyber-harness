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
    local env_file prefix_win root_win
    env_file="$(cygpath -u "${GITHUB_ENV}")"
    prefix_win="$(cygpath -m "${PREFIX}")"
    root_win="$(cygpath -m "${ROOT}")"
    echo "PKG_CONFIG_PATH=${prefix_win}/lib/pkgconfig" >> "${env_file}"
    echo "PKG_CONFIG=${root_win}/.github/native/pkg-config-static.cmd" >> "${env_file}"
    echo "CGO_CFLAGS=-I${prefix_win}/include" >> "${env_file}"
    echo "CGO_LDFLAGS=-L${prefix_win}/lib -static -static-libgcc" >> "${env_file}"
  fi
}

if [[ -f "${STAMP}" ]] && [[ "$(cat "${STAMP}")" == "${EXPECTED}" ]]; then
  echo "record native dependencies already built at ${PREFIX}"
  emit_env
  exit 0
fi

mkdir -p "${SOURCE_ROOT}" "${PREFIX}"

checkout_source() {
  local directory="$1" repository="$2" commit="$3"
  if [[ -f "${directory}/.source-commit" ]] && [[ "$(cat "${directory}/.source-commit")" == "${commit}" ]]; then
    return
  fi
  if [[ ! -d "${directory}/.git" ]]; then
    mkdir -p "${directory}"
    git -C "${directory}" init
    git -C "${directory}" remote add origin "${repository}"
  else
    git -C "${directory}" remote set-url origin "${repository}"
  fi
  if ! git -C "${directory}" cat-file -e "${commit}^{commit}" 2>/dev/null; then
    git -C "${directory}" fetch --depth 1 origin "${commit}"
  fi
  git -C "${directory}" checkout --detach "${commit}"
}

checkout_source "${SOURCE_ROOT}/x264" "${X264_REPOSITORY}" "${X264_COMMIT}"
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

checkout_source "${SOURCE_ROOT}/ffmpeg" "${FFMPEG_REPOSITORY}" "${FFMPEG_COMMIT}"
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
