#!/usr/bin/env bash
# Install the prebuilt native SDKs this repository links against.
#
# The SDKs are built and published by chainreactors/native; this script only
# downloads, verifies, and unpacks them. Building them from source is a
# maintainer task owned by that repository.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROOT}/.github/native/versions.env"

usage() {
  cat >&2 <<'EOF'
usage: sdk.sh fetch <record|re2> [os] [arch]
       sdk.sh env   <record|re2> [os] [arch]

  record  static FFmpeg + x264 for the optional record tool (linux, windows)
  re2     static libre2_cre2.a for the re2_cgo re2_static build (linux, windows, darwin)

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

# Sets: SDK_OS SDK_ARCH SDK_ARCHIVE SDK_RELEASE SDK_REPOSITORY SDK_PREFIX SDK_EXPECTED
describe_sdk() {
  local family="$1" os="$2" arch="$3"
  case "${family}/${os}/${arch}" in
    record/linux/amd64|record/linux/arm64|record/windows/amd64)
      SDK_OS="${os}"
      SDK_ARCH="${arch}"
      SDK_ARCHIVE="aiscan-record-native-${RECORD_NATIVE_VERSION}-${os}-${arch}.tar.gz"
      SDK_RELEASE="${RECORD_NATIVE_RELEASE}"
      SDK_REPOSITORY="${RECORD_NATIVE_REPOSITORY}"
      SDK_PREFIX="${CYBER_RECORD_PREFIX:-${ROOT}/.cache/record-native/${os}-${arch}}"
      SDK_EXPECTED="bundle=${RECORD_NATIVE_VERSION} platform=${os} arch=${arch}"
      ;;
    re2/linux/amd64|re2/linux/arm64|re2/windows/amd64|re2/darwin/amd64|re2/darwin/arm64)
      SDK_OS="${os}"
      SDK_ARCH="${arch}"
      SDK_ARCHIVE="native-re2-static-${RE2_STATIC_VERSION}-${os}_${arch}.tar.gz"
      SDK_RELEASE="${RE2_STATIC_RELEASE}"
      SDK_REPOSITORY="${RE2_NATIVE_REPOSITORY}"
      SDK_PREFIX="${CYBER_RE2_PREFIX:-${ROOT}/.cache/re2-static/${os}_${arch}}"
      SDK_EXPECTED="bundle=${RE2_STATIC_VERSION} platform=${os}_${arch} re2=${RE2_VERSION}"
      ;;
    record/darwin/*)
      echo "the record native backend is not supported on darwin" >&2
      exit 1
      ;;
    *)
      echo "unsupported ${family} SDK target ${os}/${arch}" >&2
      exit 1
      ;;
  esac
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
  local family="$1"
  if [[ "${SDK_OS}" == windows ]] && command -v cygpath >/dev/null 2>&1; then
    SDK_PREFIX_UNIX="$(cygpath -m "${SDK_PREFIX}")"
    SDK_ROOT_UNIX="$(cygpath -m "${ROOT}")"
  else
    SDK_PREFIX_UNIX="${SDK_PREFIX}"
    SDK_ROOT_UNIX="${ROOT}"
  fi
  case "${family}" in
    record)
      export PKG_CONFIG_PATH="${SDK_PREFIX_UNIX}/lib/pkgconfig"
      export CGO_CFLAGS="-I${SDK_PREFIX_UNIX}/include"
      if [[ "${SDK_OS}" == windows ]]; then
        export PKG_CONFIG="${SDK_ROOT_UNIX}/.github/native/pkg-config-static.cmd"
        export CGO_LDFLAGS="-L${SDK_PREFIX_UNIX}/lib -static -static-libgcc"
      else
        export PKG_CONFIG="${SDK_ROOT_UNIX}/.github/native/pkg-config-static.sh"
        export CGO_LDFLAGS="-L${SDK_PREFIX_UNIX}/lib"
      fi
      ;;
    re2)
      # The re2_static cgo directives carry every link flag except the search
      # path, so this is the only thing the SDK needs to contribute.
      export CGO_LDFLAGS="-L${SDK_PREFIX_UNIX}/lib"
      ;;
  esac
}

emit_link_env() {
  local family="$1"
  if [[ "${family}" == record ]]; then
    printf '%s\n' \
      "PKG_CONFIG=${PKG_CONFIG}" \
      "PKG_CONFIG_PATH=${PKG_CONFIG_PATH}" \
      "CGO_CFLAGS=${CGO_CFLAGS}" \
      "CGO_LDFLAGS=${CGO_LDFLAGS}"
  else
    printf '%s\n' "CGO_LDFLAGS=${CGO_LDFLAGS}"
  fi
}

fetch_sdk() {
  local family="$1" prefix stamp tmp stage backup cleanup_cmd
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
  base_url="${CYBER_NATIVE_URL:-https://github.com/${SDK_REPOSITORY}/releases/download/${SDK_RELEASE}}"

  tmp="$(mktemp -d)"
  stage="${prefix}.tmp.$$"
  backup="${prefix}.old.$$"
  printf -v cleanup_cmd 'rm -rf -- %q %q' "${tmp}" "${stage}"
  trap "${cleanup_cmd}" EXIT

  echo "downloading ${family} native SDK ${SDK_ARCHIVE}"
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
      fetch) fetch_sdk "${family}" ;;
      env) configure_link_env "${family}"; emit_link_env "${family}" ;;
    esac
    ;;
  *) usage ;;
esac
