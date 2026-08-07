#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

detect_platform() {
  case "$(uname -s)" in
    Linux*) echo linux ;;
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    *) echo unsupported ;;
  esac
}

PLATFORM="${1:-$(detect_platform)}"
ARCH="${2:-$(go env GOARCH)}"
case "${PLATFORM}/${ARCH}" in
  linux/amd64|linux/arm64|windows/amd64) ;;
  *)
    echo "no prebuilt recorder SDK for ${PLATFORM}/${ARCH}" >&2
    exit 1
    ;;
esac

PREFIX="${AISCAN_RECORD_PREFIX:-${ROOT}/.cache/record-native/${PLATFORM}-${ARCH}}"
ARCHIVE="aiscan-record-native-${RECORD_NATIVE_VERSION}-${PLATFORM}-${ARCH}.tar.gz"
BASE_URL="${AISCAN_RECORD_NATIVE_URL:-https://github.com/${RECORD_NATIVE_REPOSITORY}/releases/download/${RECORD_NATIVE_RELEASE}}"
EXPECTED="bundle=${RECORD_NATIVE_VERSION} platform=${PLATFORM} arch=${ARCH} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
STAMP="${PREFIX}/.versions"

emit_env() {
  if [[ -z "${GITHUB_ENV:-}" ]]; then
    return
  fi
  if [[ "${PLATFORM}" == "windows" ]] && command -v cygpath >/dev/null 2>&1; then
    local prefix_native root_native
    prefix_native="$(cygpath -w "${PREFIX}")"
    root_native="$(cygpath -w "${ROOT}")"
    echo "PKG_CONFIG_PATH=${prefix_native}\\lib\\pkgconfig" >> "${GITHUB_ENV}"
    echo "PKG_CONFIG=${root_native}\\.github\\native\\pkg-config-static.cmd" >> "${GITHUB_ENV}"
    echo "CGO_CFLAGS=-I${prefix_native}\\include" >> "${GITHUB_ENV}"
    echo "CGO_LDFLAGS=-L${prefix_native}\\lib -static -static-libgcc" >> "${GITHUB_ENV}"
  else
    echo "PKG_CONFIG_PATH=${PREFIX}/lib/pkgconfig" >> "${GITHUB_ENV}"
    echo "PKG_CONFIG=${ROOT}/.github/native/pkg-config-static.sh" >> "${GITHUB_ENV}"
    echo "CGO_CFLAGS=-I${PREFIX}/include" >> "${GITHUB_ENV}"
    echo "CGO_LDFLAGS=-L${PREFIX}/lib" >> "${GITHUB_ENV}"
  fi
}

if [[ -f "${STAMP}" ]] && [[ "$(cat "${STAMP}")" == "${EXPECTED}" ]]; then
  echo "record native SDK already available at ${PREFIX}"
  emit_env
  exit 0
fi

if [[ "${AISCAN_RECORD_OFFLINE:-0}" == "1" ]]; then
  echo "record native SDK is not cached at ${PREFIX} and offline mode is enabled" >&2
  exit 1
fi

for command_name in curl tar; do
  if ! command -v "${command_name}" >/dev/null 2>&1; then
    echo "${command_name} is required to download the recorder SDK" >&2
    exit 1
  fi
done

case "${PREFIX}" in
  ""|/|"${HOME:-__missing__}"|"${ROOT}")
    echo "refusing unsafe recorder SDK prefix: ${PREFIX}" >&2
    exit 1
    ;;
esac

TMP="$(mktemp -d)"
STAGE="${PREFIX}.tmp.$$"
BACKUP="${PREFIX}.old.$$"
cleanup() {
  rm -rf "${TMP}" "${STAGE}"
}
trap cleanup EXIT

echo "downloading recorder SDK ${RECORD_NATIVE_VERSION} for ${PLATFORM}/${ARCH}"
curl --fail --location --retry 3 --retry-delay 2 \
  "${BASE_URL}/${ARCHIVE}" -o "${TMP}/${ARCHIVE}"
curl --fail --location --retry 3 --retry-delay 2 \
  "${BASE_URL}/${ARCHIVE}.sha256" -o "${TMP}/${ARCHIVE}.sha256"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${TMP}" && sha256sum --check "${ARCHIVE}.sha256")
elif command -v shasum >/dev/null 2>&1; then
  (cd "${TMP}" && shasum -a 256 --check "${ARCHIVE}.sha256")
else
  echo "sha256sum or shasum is required to verify the recorder SDK" >&2
  exit 1
fi

mkdir -p "$(dirname "${PREFIX}")"
rm -rf "${STAGE}" "${BACKUP}"
mkdir -p "${STAGE}"
tar -xzf "${TMP}/${ARCHIVE}" -C "${STAGE}"

if [[ ! -f "${STAGE}/.versions" ]] || [[ "$(cat "${STAGE}/.versions")" != "${EXPECTED}" ]]; then
  echo "recorder SDK manifest does not match the requested version" >&2
  exit 1
fi
for library in avcodec avdevice avfilter avformat avutil swresample swscale x264; do
  if [[ ! -f "${STAGE}/lib/lib${library}.a" ]]; then
    echo "recorder SDK archive is missing lib${library}.a" >&2
    exit 1
  fi
done
if [[ ! -d "${STAGE}/include/libavcodec" ]]; then
  echo "recorder SDK archive is missing FFmpeg headers" >&2
  exit 1
fi

if [[ -e "${PREFIX}" ]]; then
  mv "${PREFIX}" "${BACKUP}"
fi
if ! mv "${STAGE}" "${PREFIX}"; then
  if [[ -e "${BACKUP}" ]]; then
    mv "${BACKUP}" "${PREFIX}"
  fi
  exit 1
fi
rm -rf "${BACKUP}"
echo "record native SDK installed at ${PREFIX}"
emit_env
