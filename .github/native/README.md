# Recorder native SDK

AIScan uses a two-stage build so ordinary full builds do not compile FFmpeg and x264.

1. Maintainers run the `recorder-native-sdk` workflow after changing `versions.env` or the native build configuration. It builds the pinned sources, creates relocatable static SDK archives, writes SHA-256 sidecars, and publishes the assets to the versioned GitHub release.
2. Users and product release jobs run `make full`. Its `record-native` prerequisite downloads the matching platform archive once, verifies it, and installs it below `.cache/record-native` before the Go/CGO link step. Pull-request CI falls back to the same pinned source builder when a new versioned SDK release has not been published yet.

Supported bundles are `linux-amd64`, `linux-arm64`, and `windows-amd64`. FFmpeg and x264 are static, so the distributed executable does not require separate FFmpeg/x264 installation. Operating-system libraries remain external dependencies: Linux uses glibc and X11/XCB; Windows uses system DLLs. The source builder uses an explicit component allowlist (capture input, H.264 encoder, MP4 muxer, and file output only), and packaging rejects static-library sets larger than 16 MiB by default.

The Makefile is the public build interface:

```bash
make full                              # fetch SDK and build aiscan-full
make record-native                     # fetch and verify SDK only
make record-native-source              # build SDK from pinned sources
make record-native-source record-native-package
make record-native-source RECORD_ARCH=arm64
```

Maintainers and CI use the single underlying script when they need an individual stage:

```bash
bash .github/native/sdk.sh fetch linux amd64
bash .github/native/sdk.sh build linux amd64
bash .github/native/sdk.sh package linux amd64 dist/native
bash .github/native/sdk.sh env linux amd64
```

Environment overrides:

- `AISCAN_RECORD_PREFIX`: SDK install/cache directory.
- `AISCAN_RECORD_NATIVE_URL`: release or mirror base URL containing the archive and `.sha256` sidecar.
- `AISCAN_RECORD_OFFLINE=1`: forbid downloads and require an already cached matching SDK.
- `AISCAN_RECORD_BUILD_FROM_SOURCE=1`: make `make full` or `build.sh -p full` use the pinned source builders instead of downloading an SDK.
- `RECORD_ARCH`: target architecture for Makefile SDK targets (defaults to `go env GOARCH`).
- `RECORD_NATIVE_OUTPUT`: package output directory (defaults to `dist/native`).

When native inputs or flags change, increment `RECORD_NATIVE_VERSION` and `RECORD_NATIVE_RELEASE` together before publishing. Do not replace an existing SDK version with incompatible contents.
