#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

usage() {
  cat >&2 <<'EOF'
usage: sdk.sh fetch [linux|windows] [amd64|arm64]
       sdk.sh build [linux|windows] [amd64|arm64]
       sdk.sh package <linux|windows> <amd64|arm64> [output-dir]
       sdk.sh env <linux|windows> <amd64|arm64>
EOF
  exit 2
}

detect_platform() {
  case "$(uname -s)" in
    Linux*) echo linux ;;
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    *) echo unsupported ;;
  esac
}

detect_arch() {
  if [[ -n "${GOARCH:-}" ]]; then
    echo "${GOARCH}"
  elif command -v go >/dev/null 2>&1; then
    go env GOARCH
  else
    case "$(uname -m)" in
      x86_64|amd64) echo amd64 ;;
      aarch64|arm64) echo arm64 ;;
      *) echo unsupported ;;
    esac
  fi
}

validate_target() {
  case "$1/$2" in
    linux/amd64|linux/arm64|windows/amd64) ;;
    *) echo "unsupported recorder SDK target $1/$2" >&2; exit 1 ;;
  esac
}

native_prefix() {
  local platform="$1" arch="$2"
  echo "${AISCAN_RECORD_PREFIX:-${ROOT}/.cache/record-native/${platform}-${arch}}"
}

configure_link_env() {
  local platform="$1" arch="$2" prefix root_native
  prefix="$(native_prefix "${platform}" "${arch}")"
  root_native="${ROOT}"
  if [[ "${platform}" == windows ]] && command -v cygpath >/dev/null 2>&1; then
    prefix="$(cygpath -m "${prefix}")"
    root_native="$(cygpath -m "${root_native}")"
  fi
  export PKG_CONFIG_PATH="${prefix}/lib/pkgconfig"
  export CGO_CFLAGS="-I${prefix}/include"
  if [[ "${platform}" == windows ]]; then
    export PKG_CONFIG="${root_native}/.github/native/pkg-config-static.cmd"
    export CGO_LDFLAGS="-L${prefix}/lib -static -static-libgcc"
  else
    export PKG_CONFIG="${root_native}/.github/native/pkg-config-static.sh"
    export CGO_LDFLAGS="-L${prefix}/lib"
  fi
}

emit_link_env() {
  printf '%s\n' \
    "PKG_CONFIG_PATH=${PKG_CONFIG_PATH}" \
    "PKG_CONFIG=${PKG_CONFIG}" \
    "CGO_CFLAGS=${CGO_CFLAGS}" \
    "CGO_LDFLAGS=${CGO_LDFLAGS}"
}

checkout_source() {
  local directory="$1" repository="$2" commit="$3"
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

enabled_components() {
  local config="$1" kind="$2"
  sed -nE "s/^#define CONFIG_([A-Z0-9_]+)_${kind} 1$/\\1/p" "${config}" \
    | LC_ALL=C sort \
    | paste -sd, -
}

expect_components() {
  local config="$1" kind="$2" expected="$3" actual
  actual="$(enabled_components "${config}" "${kind}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "unexpected enabled FFmpeg ${kind,,} components" >&2
    echo "expected: ${expected:-<none>}" >&2
    echo "actual:   ${actual:-<none>}" >&2
    exit 1
  fi
}

verify_ffmpeg() {
  local platform="$1" config="$2"
  [[ -f "${config}" ]] || { echo "FFmpeg component config not found: ${config}" >&2; exit 1; }
  if [[ "${platform}" == windows ]]; then
    expect_components "${config}" DECODER BMP
    expect_components "${config}" INDEV GDIGRAB
  else
    expect_components "${config}" DECODER RAWVIDEO
    expect_components "${config}" INDEV XCBGRAB
  fi
  expect_components "${config}" ENCODER LIBX264
  expect_components "${config}" MUXER MOV,MP4
  expect_components "${config}" DEMUXER ""
  expect_components "${config}" PROTOCOL FILE
  expect_components "${config}" FILTER ""
  expect_components "${config}" OUTDEV ""
  expect_components "${config}" PARSER AC3
  expect_components "${config}" BSF AAC_ADTSTOASC,VP9_SUPERFRAME
  echo "verified minimal FFmpeg component set for ${platform}"
}

fetch_sdk() {
  local platform="$1" arch="$2" prefix archive base_url expected stamp
  prefix="$(native_prefix "${platform}" "${arch}")"
  archive="aiscan-record-native-${RECORD_NATIVE_VERSION}-${platform}-${arch}.tar.gz"
  base_url="${AISCAN_RECORD_NATIVE_URL:-https://github.com/${RECORD_NATIVE_REPOSITORY}/releases/download/${RECORD_NATIVE_RELEASE}}"
  expected="bundle=${RECORD_NATIVE_VERSION} platform=${platform} arch=${arch} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
  stamp="${prefix}/.versions"

  if [[ -f "${stamp}" ]] && [[ "$(cat "${stamp}")" == "${expected}" ]]; then
    echo "record native SDK already available at ${prefix}"
    return
  fi
  if [[ "${AISCAN_RECORD_OFFLINE:-0}" == 1 ]]; then
    echo "record native SDK is not cached at ${prefix} and offline mode is enabled" >&2
    exit 1
  fi
  for command_name in curl tar; do
    command -v "${command_name}" >/dev/null 2>&1 || { echo "${command_name} is required to download the recorder SDK" >&2; exit 1; }
  done
  case "${prefix}" in
    ""|/|"${HOME:-__missing__}"|"${ROOT}") echo "refusing unsafe recorder SDK prefix: ${prefix}" >&2; exit 1 ;;
  esac

  local tmp stage backup cleanup_cmd
  tmp="$(mktemp -d)"
  stage="${prefix}.tmp.$$"
  backup="${prefix}.old.$$"
  printf -v cleanup_cmd 'rm -rf -- %q %q' "${tmp}" "${stage}"
  trap "${cleanup_cmd}" EXIT

  echo "downloading recorder SDK ${RECORD_NATIVE_VERSION} for ${platform}/${arch}"
  curl --fail --location --connect-timeout 20 --speed-time 30 --speed-limit 1024 \
    --retry 5 --retry-delay 2 --retry-all-errors "${base_url}/${archive}" -o "${tmp}/${archive}"
  curl --fail --location --connect-timeout 20 --speed-time 30 --speed-limit 1024 \
    --retry 5 --retry-delay 2 --retry-all-errors "${base_url}/${archive}.sha256" -o "${tmp}/${archive}.sha256"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${tmp}" && sha256sum --check "${archive}.sha256")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "${tmp}" && shasum -a 256 --check "${archive}.sha256")
  else
    echo "sha256sum or shasum is required to verify the recorder SDK" >&2
    exit 1
  fi

  mkdir -p "$(dirname "${prefix}")"
  rm -rf "${stage}" "${backup}"
  mkdir -p "${stage}"
  tar -xzf "${tmp}/${archive}" -C "${stage}"
  if [[ ! -f "${stage}/.versions" ]] || [[ "$(cat "${stage}/.versions")" != "${expected}" ]]; then
    echo "recorder SDK manifest does not match the requested version" >&2
    exit 1
  fi
  for library in avcodec avdevice avfilter avformat avutil swresample swscale x264; do
    [[ -f "${stage}/lib/lib${library}.a" ]] || { echo "recorder SDK archive is missing lib${library}.a" >&2; exit 1; }
  done
  [[ -d "${stage}/include/libavcodec" ]] || { echo "recorder SDK archive is missing FFmpeg headers" >&2; exit 1; }

  [[ ! -e "${prefix}" ]] || mv "${prefix}" "${backup}"
  if ! mv "${stage}" "${prefix}"; then
    [[ ! -e "${backup}" ]] || mv "${backup}" "${prefix}"
    exit 1
  fi
  rm -rf "${backup}" "${tmp}"
  trap - EXIT
  echo "record native SDK installed at ${prefix}"
}

install_licenses() {
  local prefix="$1" source_root="$2"
  mkdir -p "${prefix}/share/licenses/ffmpeg" "${prefix}/share/licenses/x264"
  for license in COPYING.GPLv2 COPYING.GPLv3 LICENSE.md; do
    [[ ! -f "${source_root}/ffmpeg/${license}" ]] || cp "${source_root}/ffmpeg/${license}" "${prefix}/share/licenses/ffmpeg/"
  done
  [[ ! -f "${source_root}/x264/COPYING" ]] || cp "${source_root}/x264/COPYING" "${prefix}/share/licenses/x264/"
}

build_sdk() {
  local platform="$1" arch="$2" prefix source_root stamp expected
  prefix="$(native_prefix "${platform}" "${arch}")"
  source_root="${AISCAN_RECORD_SOURCE:-${ROOT}/.cache/record-native/src}"
  stamp="${prefix}/.versions"
  expected="source_bundle=${RECORD_NATIVE_VERSION} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
  if [[ -f "${stamp}" ]] && [[ "$(cat "${stamp}")" == "${expected}" ]]; then
    echo "record native dependencies already built at ${prefix}"
    return
  fi

  local -a x264_platform_args ffmpeg_platform_args
  if [[ "${platform}" == windows ]]; then
    export MSYSTEM=MINGW64
    export PATH="/mingw64/bin:/usr/bin:${PATH}"
    gcc -dumpmachine | grep -q 'mingw32$' || { echo "a MinGW-w64 GCC toolchain is required" >&2; exit 1; }
    x264_platform_args=(--host=x86_64-w64-mingw32)
    ffmpeg_platform_args=(--enable-indev=gdigrab)
  else
    x264_platform_args=(--enable-pic)
    ffmpeg_platform_args=(
      --enable-pic --enable-indev=xcbgrab --enable-decoder=rawvideo
      --enable-libxcb --enable-libxcb-shm --enable-libxcb-shape --enable-libxcb-xfixes
    )
  fi

  mkdir -p "${source_root}" "${prefix}"
  checkout_source "${source_root}/x264" "${X264_REPOSITORY}" "${X264_COMMIT}"
  (
    cd "${source_root}/x264"
    make distclean >/dev/null 2>&1 || true
    ./configure \
      --prefix="${prefix}" \
      --enable-static --disable-cli \
      --bit-depth=8 --chroma-format=420 \
      --disable-opencl --disable-interlaced \
      "${x264_platform_args[@]}"
    make -j"$(nproc)"
    make install
  )

  checkout_source "${source_root}/ffmpeg" "${FFMPEG_REPOSITORY}" "${FFMPEG_COMMIT}"
  (
    cd "${source_root}/ffmpeg"
    make distclean >/dev/null 2>&1 || true
    PKG_CONFIG_PATH="${prefix}/lib/pkgconfig" ./configure \
      --prefix="${prefix}" \
      --disable-shared --enable-static \
      --disable-programs --disable-doc --disable-debug --disable-network \
      --disable-autodetect --disable-everything \
      --enable-gpl --enable-libx264 \
      --enable-encoder=libx264 --enable-muxer=mp4 \
      --enable-protocol=file --enable-swscale \
      --extra-cflags="-I${prefix}/include" \
      --extra-ldflags="-L${prefix}/lib" \
      "${ffmpeg_platform_args[@]}"
    verify_ffmpeg "${platform}" config_components.h
    make -j"$(nproc)"
    make install
  )

  install_licenses "${prefix}" "${source_root}"
  printf '%s' "${expected}" > "${stamp}"
  echo "record native dependencies built at ${prefix}"
}

package_sdk() {
  local platform="$1" arch="$2" output_dir="$3" prefix source_stamp bundle_stamp archive max_bytes
  prefix="$(native_prefix "${platform}" "${arch}")"
  source_stamp="source_bundle=${RECORD_NATIVE_VERSION} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
  bundle_stamp="bundle=${RECORD_NATIVE_VERSION} platform=${platform} arch=${arch} ffmpeg=${FFMPEG_COMMIT} x264=${X264_COMMIT}"
  archive="aiscan-record-native-${RECORD_NATIVE_VERSION}-${platform}-${arch}.tar.gz"
  max_bytes="${AISCAN_RECORD_MAX_LIB_BYTES:-16777216}"
  if [[ ! -f "${prefix}/.versions" ]] || [[ "$(cat "${prefix}/.versions")" != "${source_stamp}" ]]; then
    echo "native dependencies at ${prefix} do not match versions.env" >&2
    exit 1
  fi

  local static_bytes=0 bytes
  while IFS= read -r -d '' library; do
    bytes="$(wc -c < "${library}")"
    static_bytes=$((static_bytes + bytes))
  done < <(find "${prefix}/lib" -maxdepth 1 -type f -name '*.a' -print0)
  if (( static_bytes > max_bytes )); then
    echo "recorder static libraries are ${static_bytes} bytes; budget is ${max_bytes}" >&2
    echo "the FFmpeg component allowlist may have regressed" >&2
    exit 1
  fi

  local tmp stage cleanup_cmd
  tmp="$(mktemp -d)"
  stage="${tmp}/sdk"
  printf -v cleanup_cmd 'rm -rf -- %q' "${tmp}"
  trap "${cleanup_cmd}" EXIT
  mkdir -p "${stage}" "${output_dir}"
  cp -R "${prefix}/include" "${prefix}/lib" "${stage}/"
  if [[ -d "${prefix}/share/licenses" ]]; then
    mkdir -p "${stage}/share"
    cp -R "${prefix}/share/licenses" "${stage}/share/"
  fi
  if [[ -d "${stage}/lib/pkgconfig" ]]; then
    while IFS= read -r -d '' pc; do
      sed -i.bak 's|^prefix=.*|prefix=${pcfiledir}/../..|' "${pc}"
      rm -f "${pc}.bak"
    done < <(find "${stage}/lib/pkgconfig" -type f -name '*.pc' -print0)
  fi

  printf '%s' "${bundle_stamp}" > "${stage}/.versions"
  cat > "${stage}/README.txt" <<EOF
AIScan recorder native SDK ${RECORD_NATIVE_VERSION}
Target: ${platform}/${arch}
FFmpeg: ${FFMPEG_TAG} (${FFMPEG_COMMIT})
x264: ${X264_COMMIT}

This bundle contains size-bounded, feature-minimal static FFmpeg and x264
development libraries for AIScan recording. OS system libraries remain
external platform dependencies.
Static library bytes: ${static_bytes}
EOF
  tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
    -C "${stage}" -cf - . | gzip -n > "${output_dir}/${archive}"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${output_dir}" && sha256sum "${archive}" > "${archive}.sha256")
  else
    local digest
    digest="$(shasum -a 256 "${output_dir}/${archive}" | awk '{print $1}')"
    printf '%s  %s\n' "${digest}" "${archive}" > "${output_dir}/${archive}.sha256"
  fi
  rm -rf "${tmp}"
  trap - EXIT
  echo "packaged ${output_dir}/${archive}"
}

command_name="${1:-}"
case "${command_name}" in
  fetch|build|env)
    platform="${2:-$(detect_platform)}"
    arch="${3:-$(detect_arch)}"
    validate_target "${platform}" "${arch}"
    case "${command_name}" in
      fetch) fetch_sdk "${platform}" "${arch}" ;;
      build) build_sdk "${platform}" "${arch}" ;;
      env) configure_link_env "${platform}" "${arch}"; emit_link_env ;;
    esac
    ;;
  package)
    [[ $# -ge 3 ]] || usage
    platform="$2"
    arch="$3"
    validate_target "${platform}" "${arch}"
    package_sdk "${platform}" "${arch}" "${4:-${ROOT}/dist/native}"
    ;;
  *) usage ;;
esac
