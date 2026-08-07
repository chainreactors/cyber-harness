#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

PLATFORM="${1:?usage: package.sh <linux|windows> <amd64|arm64> [output-dir]}"
ARCH="${2:?usage: package.sh <linux|windows> <amd64|arm64> [output-dir]}"
OUTPUT_DIR="${3:-${ROOT}/dist/native}"
PREFIX="${AISCAN_RECORD_PREFIX:-${ROOT}/.cache/record-native/${PLATFORM}-${ARCH}}"
SOURCE_STAMP="source_bundle=${RECORD_NATIVE_VERSION} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
BUNDLE_STAMP="bundle=${RECORD_NATIVE_VERSION} platform=${PLATFORM} arch=${ARCH} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
ARCHIVE="aiscan-record-native-${RECORD_NATIVE_VERSION}-${PLATFORM}-${ARCH}.tar.gz"
MAX_STATIC_LIB_BYTES="${AISCAN_RECORD_MAX_LIB_BYTES:-16777216}"

case "${PLATFORM}/${ARCH}" in
  linux/amd64|linux/arm64|windows/amd64) ;;
  *) echo "unsupported recorder SDK target ${PLATFORM}/${ARCH}" >&2; exit 1 ;;
esac
if [[ ! -f "${PREFIX}/.versions" ]] || [[ "$(cat "${PREFIX}/.versions")" != "${SOURCE_STAMP}" ]]; then
  echo "native dependencies at ${PREFIX} do not match versions.env" >&2
  exit 1
fi

static_lib_bytes=0
while IFS= read -r -d '' library; do
  bytes="$(wc -c < "${library}")"
  static_lib_bytes=$((static_lib_bytes + bytes))
done < <(find "${PREFIX}/lib" -maxdepth 1 -type f -name '*.a' -print0)
if (( static_lib_bytes > MAX_STATIC_LIB_BYTES )); then
  echo "recorder static libraries are ${static_lib_bytes} bytes; budget is ${MAX_STATIC_LIB_BYTES}" >&2
  echo "the FFmpeg component allowlist may have regressed" >&2
  exit 1
fi

TMP="$(mktemp -d)"
cleanup() { rm -rf "${TMP}"; }
trap cleanup EXIT
STAGE="${TMP}/sdk"
mkdir -p "${STAGE}" "${OUTPUT_DIR}"
cp -R "${PREFIX}/include" "${PREFIX}/lib" "${STAGE}/"
if [[ -d "${PREFIX}/share/licenses" ]]; then
  mkdir -p "${STAGE}/share"
  cp -R "${PREFIX}/share/licenses" "${STAGE}/share/"
fi

# Installed pkg-config files contain the maintainer's absolute build prefix.
# Make the SDK relocatable before publishing it.
if [[ -d "${STAGE}/lib/pkgconfig" ]]; then
  while IFS= read -r -d '' pc; do
    sed -i.bak 's|^prefix=.*|prefix=${pcfiledir}/../..|' "${pc}"
    rm -f "${pc}.bak"
  done < <(find "${STAGE}/lib/pkgconfig" -type f -name '*.pc' -print0)
fi

printf '%s' "${BUNDLE_STAMP}" > "${STAGE}/.versions"
cat > "${STAGE}/README.txt" <<EOF
AIScan recorder native SDK ${RECORD_NATIVE_VERSION}
Target: ${PLATFORM}/${ARCH}
FFmpeg: ${FFMPEG_TAG} (${FFMPEG_COMMIT})
x264: ${X264_COMMIT}

This bundle contains size-bounded, feature-minimal static FFmpeg and x264
development libraries for AIScan recording. OS system libraries remain
external platform dependencies.
Static library bytes: ${static_lib_bytes}
EOF

tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
  -C "${STAGE}" -cf - . | gzip -n > "${OUTPUT_DIR}/${ARCHIVE}"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "${OUTPUT_DIR}" && sha256sum "${ARCHIVE}" > "${ARCHIVE}.sha256")
else
  digest="$(shasum -a 256 "${OUTPUT_DIR}/${ARCHIVE}" | awk '{print $1}')"
  printf '%s  %s\n' "${digest}" "${ARCHIVE}" > "${OUTPUT_DIR}/${ARCHIVE}.sha256"
fi
echo "packaged ${OUTPUT_DIR}/${ARCHIVE}"
