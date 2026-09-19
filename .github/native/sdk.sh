#!/usr/bin/env bash
# Install the prebuilt recorder SDK this repository links against.
#
# The SDK is built and published by chainreactors/native; this script only
# downloads, verifies, and unpacks it. Building it from source is a
# maintainer task owned by that repository.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

usage() {
  cat >&2 <<'EOF'
usage: sdk.sh fetch record [os] [arch]
       sdk.sh env   record [os] [arch]

  record  static record FFI + FFmpeg/x264 for the record Extension (linux, windows)

OS and architecture default to the current host. `env` prints the CGO
environment that points at the installed prefix; append it to $GITHUB_ENV in CI.
EOF
  exit 2
}

detect_os() {
  case "$(uname -s)" in
    Linux*) echo linux ;;
    Darwin*) echo darwin ;;
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

manifest_field() {
  printf '%s\n' "$1" | tr ' ' '\n' | sed -n "s/^$2=//p"
}

validate_target() {
  case "$1/$2" in
    linux/amd64|linux/arm64|windows/amd64) ;;
    darwin/*)
      echo "the record native backend is not supported on darwin" >&2
      exit 1
      ;;
    *)
      echo "unsupported record SDK target $1/$2" >&2
      exit 1
      ;;
  esac
}

# Sets: SDK_FAMILY SDK_OS SDK_ARCH SDK_VERSION SDK_RELEASE SDK_ARCHIVE
#       SDK_PREFIX SDK_EXPECTED
describe_sdk() {
  local family="$1" os="$2" arch="$3" override
  if [[ "${family}" != record ]]; then
    usage
  fi
  validate_target "${os}" "${arch}"
  SDK_FAMILY=record
  SDK_OS="${os}"
  SDK_ARCH="${arch}"
  SDK_VERSION="${RECORD_NATIVE_VERSION}"
  SDK_RELEASE="${RECORD_NATIVE_RELEASE}"
  SDK_ARCHIVE="native-record-${SDK_VERSION}-${os}_${arch}.tar.gz"
  SDK_EXPECTED="family=record version=${SDK_VERSION} platform=${os}_${arch}"
  override="${CYBER_RECORD_PREFIX-}"
  SDK_PREFIX="${override:-${ROOT}/.cache/native/record/${os}_${arch}}"
}

# The release manifest is a superset of what this repository pins, so compare
# only the fields we track rather than the whole string.
verify_manifest() {
  local manifest_file="$1" expected="$2" key want got
  if [[ ! -f "${manifest_file}" ]]; then
    echo "native SDK archive has no .versions manifest" >&2
    exit 1
  fi
  for field in ${expected}; do
    key="${field%%=*}"
    want="${field#*=}"
    got="$(manifest_field "$(cat "${manifest_file}")" "${key}")"
    if [[ "${got}" != "${want}" ]]; then
      echo "native SDK manifest ${key} is '${got}', expected '${want}'" >&2
      exit 1
    fi
  done
}

configure_link_env() {
  if [[ "${SDK_OS}" == windows ]] && command -v cygpath >/dev/null 2>&1; then
    SDK_PREFIX_UNIX="$(cygpath -m "${SDK_PREFIX}")"
  else
    SDK_PREFIX_UNIX="${SDK_PREFIX}"
  fi
  if [[ "${SDK_OS}" == windows ]]; then
    export CGO_LDFLAGS="-L${SDK_PREFIX_UNIX}/lib -static -static-libgcc"
  else
    export CGO_LDFLAGS="-L${SDK_PREFIX_UNIX}/lib"
  fi
}

emit_link_env() {
  printf '%s\n' "CGO_LDFLAGS=${CGO_LDFLAGS}"
}

fetch_sdk() {
  local prefix stamp tmp stage backup cleanup_cmd
  prefix="${SDK_PREFIX}"
  stamp="${prefix}/.versions"

  if [[ -f "${stamp}" ]] && verify_manifest "${stamp}" "${SDK_EXPECTED}" 2>/dev/null; then
    echo "native SDK already available at ${prefix}"
    return
  fi
  if [[ "${CYBER_NATIVE_OFFLINE:-0}" == 1 ]]; then
    echo "native SDK is not cached at ${prefix} and offline mode is enabled" >&2
    exit 1
  fi
  for command_name in curl tar; do
    command -v "${command_name}" >/dev/null 2>&1 || { echo "${command_name} is required to download the native SDK" >&2; exit 1; }
  done
  case "${prefix}" in
    ""|/|"${HOME:-__missing__}"|"${ROOT}") echo "refusing unsafe native SDK prefix: ${prefix}" >&2; exit 1 ;;
  esac

  local base_url
  base_url="${CYBER_NATIVE_URL:-https://github.com/${NATIVE_REPOSITORY}/releases/download/${SDK_RELEASE}}"

  tmp="$(mktemp -d)"
  stage="${prefix}.tmp.$$"
  backup="${prefix}.old.$$"
  printf -v cleanup_cmd 'rm -rf -- %q %q' "${tmp}" "${stage}"
  trap "${cleanup_cmd}" EXIT

  echo "downloading ${SDK_FAMILY} native SDK ${SDK_ARCHIVE}"
  curl --fail --location --connect-timeout 20 --speed-time 30 --speed-limit 1024 \
    --retry 5 --retry-delay 2 --retry-all-errors "${base_url}/${SDK_ARCHIVE}" -o "${tmp}/${SDK_ARCHIVE}"
  curl --fail --location --connect-timeout 20 --speed-time 30 --speed-limit 1024 \
    --retry 5 --retry-delay 2 --retry-all-errors "${base_url}/${SDK_ARCHIVE}.sha256" -o "${tmp}/${SDK_ARCHIVE}.sha256"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "${tmp}" && sha256sum --check "${SDK_ARCHIVE}.sha256")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "${tmp}" && shasum -a 256 --check "${SDK_ARCHIVE}.sha256")
  else
    echo "sha256sum or shasum is required to verify the native SDK" >&2
    exit 1
  fi

  mkdir -p "$(dirname "${prefix}")"
  rm -rf "${stage}" "${backup}"
  mkdir -p "${stage}"
  tar -xzf "${tmp}/${SDK_ARCHIVE}" -C "${stage}"
  verify_manifest "${stage}/.versions" "${SDK_EXPECTED}"
  [[ -d "${stage}/lib" ]] || { echo "native SDK archive is missing lib/" >&2; exit 1; }
  [[ -f "${stage}/lib/librecord.a" ]] || { echo "record SDK archive is missing librecord.a" >&2; exit 1; }
  [[ -f "${stage}/include/record_ffi.h" ]] || { echo "record SDK archive is missing record_ffi.h" >&2; exit 1; }
  cmp -s "${stage}/include/record_ffi.h" "${ROOT}/pkg/exts/record/record_ffi.h" || {
    echo "record SDK ABI header does not match pkg/exts/record/record_ffi.h" >&2
    exit 1
  }

  [[ ! -e "${prefix}" ]] || mv "${prefix}" "${backup}"
  if ! mv "${stage}" "${prefix}"; then
    [[ ! -e "${backup}" ]] || mv "${backup}" "${prefix}"
    exit 1
  fi
  rm -rf "${backup}" "${tmp}"
  trap - EXIT
  echo "native SDK installed at ${prefix}"
}

command_name="${1:-}"
family="${2:-}"
case "${command_name}" in
  fetch|env)
    os="${3:-$(detect_os)}"
    arch="${4:-$(detect_arch)}"
    describe_sdk "${family}" "${os}" "${arch}"
    case "${command_name}" in
      fetch) fetch_sdk ;;
      env) configure_link_env; emit_link_env ;;
    esac
    ;;
  *) usage ;;
esac
