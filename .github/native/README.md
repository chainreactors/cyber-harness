# Recorder native SDK

AIScan uses a two-stage build so ordinary full builds do not compile FFmpeg and x264.

1. Maintainers run the `recorder-native-sdk` workflow after changing `versions.env` or the native build configuration. It builds the pinned sources, creates relocatable static SDK archives, writes SHA-256 sidecars, and publishes the assets to the versioned GitHub release.
2. Users, CI, and product release jobs run `fetch.sh`. It downloads the matching platform archive once, verifies it, and installs it below `.cache/record-native` before the Go/CGO link step.

Supported bundles are `linux-amd64`, `linux-arm64`, and `windows-amd64`. FFmpeg and x264 are static; operating-system libraries remain external dependencies. The source builders use an explicit component allowlist (capture input, H.264 encoder, MP4 muxer, and file output only), and packaging rejects static-library sets larger than 16 MiB by default.

Commands:

```bash
# Consumer path (default for make full)
bash .github/native/fetch.sh linux amd64

# Maintainer/source path
bash .github/native/build-linux.sh
bash .github/native/package.sh linux amd64 dist/native
```

Environment overrides:

- `AISCAN_RECORD_PREFIX`: SDK install/cache directory.
- `AISCAN_RECORD_NATIVE_URL`: release or mirror base URL containing the archive and `.sha256` sidecar.
- `AISCAN_RECORD_OFFLINE=1`: forbid downloads and require an already cached matching SDK.
- `AISCAN_RECORD_BUILD_FROM_SOURCE=1`: make `make full` or `build.sh -p full` use the pinned source builders instead of downloading an SDK.

When native inputs or flags change, increment `RECORD_NATIVE_VERSION` and `RECORD_NATIVE_RELEASE` together before publishing. Do not replace an existing SDK version with incompatible contents.
