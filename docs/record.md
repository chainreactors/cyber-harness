# record — desktop and window capture

`record` is an optional native tool for SDK and tool developers. It captures PNG screenshots and H.264/MP4 recordings from the desktop or a visible application window. Default full builds do not compile or register it.

| Platform | Support |
| --- | --- |
| Windows amd64 | Supported |
| Linux amd64/arm64 with X11 | Supported |
| Wayland, macOS, Windows arm64 | Not supported |
| Headless hosts and Windows session 0 | Not supported |

Examples:

```json
{"action":"screenshot"}
{"action":"screenshot","target":"window","pid":1234}
{"action":"record","target":"window","window_handle":"0x12345","duration_seconds":10}
{"action":"start","target":"desktop","fps":30}
{"action":"stop","recording_id":"<id>"}
{"action":"status"}
```

Windows window targets use an `HWND`; Linux uses an X11 Window ID. Handles are strings and accept decimal or `0x` hexadecimal notation. A PID resolves to the largest visible, non-minimized top-level window owned by that process.

Defaults:

- Desktop target, 30 FPS, mouse cursor included.
- Screenshots are PNG; recordings are H.264/libx264 in MP4.
- Outputs are written below `.cyber/record/` unless `output` is specified.
- At most four recordings run concurrently. Set `CYBER_RECORD_MAX_CONCURRENT` to a value from 1 to 16 to change the limit.

Media transport uses the existing AOP media and file namespaces. Screenshot
previews are returned as bounded inline `Content.media` data. Completed videos
are returned as `Content.media` with a task-relative `Resource.uri`; consumers
read the underlying MP4 through chunked `aop.file` requests. When a tool
invocation supplies a work directory, the default output is
`<workdir>/.cyber/record/`, so remote runners can expose the URI without
leaking or depending on a machine-global data path.

Limitations:

- Video only; microphone and system audio are not captured.
- Wayland is not supported. Use an X11 session.
- macOS and Windows arm64 do not have a native recorder backend.
- Capture requires an interactive graphical session; headless hosts and Windows session 0 are not supported.
- The window must be visible and non-minimized. Capture size is fixed when recording starts; closing, minimizing, or shrinking the window can terminate the recording.
- The native backend is not present in official full builds. Custom builds require CGO, the `record` build tag, and a supported C toolchain.

Record-enabled builds statically link a feature-minimal FFmpeg, x264, and the
Linux XCB client code, so users do not install those runtimes separately. This
is single-file distribution, not literally zero runtime dependencies: Windows
still uses system DLLs, while Linux requires glibc and an accessible X11
`DISPLAY`. The SDK only enables the platform capture input, its raw/BMP
decoder, libx264, the MP4 muxer, file output, and pixel conversion. It is not a
general-purpose FFmpeg build.

## Build tags and CGO

Editions are defined by tags, not by which files happen to be imported. Three
editions exist; the build manifest also records the release default for CGO:

| Edition | Tags | Release CGO_ENABLED |
| --- | --- | --- |
| standard | `forceposix emptytemplates noembed osusergo netgo` | `0` |
| full | standard + `full sqlite` | `0` |
| record | full + `record` | `1` |

`editions.env` at the repository root is the source of truth for this table:
the Makefile includes it, the workflows append it to `$GITHUB_ENV`, and
`TestBuildManifestIsConsistent` and the manifest tag tests in `cmd/aiscan` fail
when a set here drifts from the file. It is a data file, not a shell script —
the values hold spaces, so sourcing it would truncate every one of them.

- **CSTX** normalization runs in the browser through `@cyber/cstx` and its
  version-matched WASM ABI. `pkg/exts/cstx` remains available as an uncomposed
  native extension, but no aiscan edition imports or starts it.
- **`record`** composes `pkg/exts/record`. That Extension owns its native
  runtime and contributes the record Tool through the typed resource scope.
- **RE2** uses the dependency's pure-Go backend. The same standard or full
  source tree builds and works with `CGO_ENABLED=0` or `CGO_ENABLED=1`; enabling
  cgo no longer selects a different RE2 implementation.
- `sqlite` is a dependency-supplied tag; the sqlite driver itself is pure Go.

CI checks that the aiscan dependency graph does not reach `libcstx` and compiles
the full edition with both cgo settings. Official standard and full releases use
`CGO_ENABLED=0` so they require no C toolchain. Record is the only edition whose
feature set requires `CGO_ENABLED=1`.

## Native SDKs

The recorder's FFmpeg/x264 backend links a prebuilt static SDK that this
repository downloads and verifies but never builds. It is published as a
`chainreactors/native` release asset:

| SDK | Target | Version pin | Release tag |
| --- | --- | --- | --- |
| Recorder | `record` | `RECORD_NATIVE_VERSION` | `RECORD_NATIVE_RELEASE` |

`.github/native/versions.env` pins the tag, and `.github/native/sdk.sh fetch
record [os] [arch]` downloads the archive, checks its SHA-256 sidecar and
`.versions` manifest, and unpacks it into `.cache/native/record/<os>_<arch>`.
`sdk.sh env record` prints the link environment for that prefix.

The archive carries one `librecord.a` plus its narrow ABI header. It already
combines the record shim, FFmpeg, x264, and Linux XCB objects; the cyber build
consumes neither FFmpeg headers nor pkg-config metadata. Install it through the
Makefile, which wires its library prefix in:

```bash
make record-native   # .cache/native/record/<os>_<arch>, for `record`
make record          # fetches it, then builds bin/aiscan-record
```

`make record` depends on the fetch target, so it needs no manual pre-step.
Building the record edition by hand means exporting
`CGO_LDFLAGS="-L<cache>/lib"` yourself, because a cgo directive can only expand
`${SRCDIR}` and the archive is outside the module.

The recorder SDK supports Linux amd64/arm64 and Windows amd64. `make record`
therefore fails on macOS, and on Windows it needs MinGW-w64 so that `gcc` can
link the MSVC-incompatible static archive.

`RECORD_ARCH` picks the SDK architecture, `CYBER_RECORD_PREFIX` overrides the
cache directory, and `CYBER_NATIVE_URL` points downloads at a mirror.

### Publishing

The SDK is built and published by `chainreactors/native`'s
`record-native-sdk.yml` workflow. It runs on a matching native runner and
publishes with that repository's own `GITHUB_TOKEN`, so no cross-repository
credential is involved on this side.

The recorder build verifies an exact FFmpeg component allowlist, and packaging
rejects static libraries above a 16 MiB budget unless `CYBER_RECORD_MAX_LIB_BYTES`
explicitly overrides it. This prevents an FFmpeg upgrade or configure change from
silently restoring all default codecs and adding tens of megabytes to
record-enabled binaries.

Native smoke tests are opt-in because they require an interactive desktop/X11 session:

```bash
go test -tags "record record_integration" ./pkg/exts/record
```
