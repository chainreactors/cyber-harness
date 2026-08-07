#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

ARCH="${GOARCH:-}"
if [[ -z "${ARCH}" ]] && command -v go >/dev/null 2>&1; then
  ARCH="$(go env GOARCH)"
fi
if [[ -z "${ARCH}" ]]; then
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "cannot determine recorder target architecture" >&2; exit 1 ;;
  esac
fi
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
    --enable-static --enable-pic --disable-cli \
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
