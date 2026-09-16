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
- The native backend is not present in official full builds. Custom builds require CGO, the `record_ffmpeg` build tag, and a supported C toolchain.

Record-enabled builds statically link a feature-minimal FFmpeg and x264, so users do not install either runtime separately. This is single-file distribution, not literally zero runtime dependencies: Windows still uses system DLLs; Linux requires glibc, X11/XCB libraries, and an accessible `DISPLAY`. The SDK only enables the platform capture input, its raw/BMP decoder, libx264, the MP4 muxer, file output, and pixel conversion. It is not a general-purpose FFmpeg build.

## Build tags and CGO

Editions are defined by tags, not by which files happen to be imported. Three
editions exist, and each one pins its CGO setting:

| Edition | Tags | CGO_ENABLED |
| --- | --- | --- |
| standard | `forceposix emptytemplates noembed osusergo netgo` | `0` |
| full | standard + `full sqlite cstx re2_cgo re2_static` | `1` |
| record | full + `record_ffmpeg` | `1` |

- **`cstx`** gates the native SCO importer in `pkg/exts/cstx`, which is the only
  package that imports `libcstx`. Every file there requires both `cstx` and
  `cgo`, so the dependency cannot leak into a pure-Go build. It is registered as
  an extension on the web layer's own `extension.Set`.
- **`full`** implies `cstx`, therefore `full` requires `CGO_ENABLED=1`. A full
  build that succeeds with `CGO_ENABLED=0` means the cstx files stopped being
  gated and the edition silently lost the native importer.
- **`re2_cgo`/`re2_static`** select the cgo RE2 backend. Without them the RE2
  binding stays on its pure-Go engine, which is slower but still builds with
  `CGO_ENABLED=0`. `re2_static` links a prebuilt `libre2_cre2.a` that does not
  live in the module tree, so it only links when the SDK is installed and its
  `lib` directory is on the linker's search path — see below.
- `sqlite` is a dependency-supplied tag; the sqlite driver itself is pure Go.

CI enforces both halves: `go list -deps` over the whole module must not reach
`libcstx` under the standard tags, and the full edition must build with
`CGO_ENABLED=1` and fail with `CGO_ENABLED=0`.

## Native SDKs

Both cgo features — the cgo RE2 backend and the recorder's FFmpeg/x264 backend —
link prebuilt static SDKs that this repository downloads and verifies but never
builds. The archives are release assets of `chainreactors/native`:

| SDK | Target | Release tag | Asset |
| --- | --- | --- | --- |
| RE2 | `re2` | `RE2_STATIC_RELEASE` | `native-re2-static-<re2ver>-<platform>.tar.gz` |
| Recorder | `record` | `RECORD_NATIVE_RELEASE` | `aiscan-record-native-<ver>-<platform>-<arch>.tar.gz` |

`.github/native/versions.env` pins the tags, and `.github/native/sdk.sh fetch
<family> [os] [arch]` downloads the archive, checks its SHA-256 sidecar and
`.versions` manifest, and unpacks it into `.cache/`. `sdk.sh env <family>` then
prints the link environment for that prefix.

The RE2 archive carries only `lib/libre2_cre2.a` and licences: the `cre2.h` its
cgo directives include lives in the Go module, so cgo supplies that include path
itself. The recorder archive also carries FFmpeg headers and pkg-config files,
because its linking is driven through `pkg-config`. Either way, installing the
SDK and pointing the linker at its `lib` directory is all a build needs. Install
them through the Makefile, which wires the prefixes in:

```bash
make re2-static      # .cache/re2-static/<os>_<arch>, for `full`
make record-native   # .cache/record-native/<os>-<arch>, for `record`
make record          # fetches both, then builds bin/aiscan-record
```

`make full` and `make record` depend on the matching fetch target, so neither
needs a manual pre-step. Building `full` or `record` by hand means exporting
`CGO_LDFLAGS="-L<cache>/lib"` yourself, because a cgo directive can only expand
`${SRCDIR}` and the archive is no longer inside the module.

Supported SDK targets: RE2 on Linux, macOS, and Windows (amd64/arm64 as
published); recorder on Linux amd64/arm64 and Windows amd64. `make record`
therefore fails on macOS, and on Windows it needs MinGW-w64 so that `gcc` can
link the MSVC-incompatible static archives.

`RECORD_ARCH`/`RE2_ARCH` pick the SDK architecture, `CYBER_RECORD_PREFIX` and
`CYBER_RE2_PREFIX` override the cache directories, and `CYBER_NATIVE_URL` points
downloads at a mirror.

### Publishing

The SDKs are built and published by `chainreactors/native`'s own workflows
(`record-native-sdk.yml`, and the static-archive rebuild workflow for RE2). Each
runs on a matching native runner and publishes with that repository's own
`GITHUB_TOKEN`, so no cross-repository credential is involved on this side.

The recorder build verifies an exact FFmpeg component allowlist, and packaging
rejects static libraries above a 16 MiB budget unless `CYBER_RECORD_MAX_LIB_BYTES`
explicitly overrides it. This prevents an FFmpeg upgrade or configure change from
silently restoring all default codecs and adding tens of megabytes to
record-enabled binaries.

Native smoke tests are opt-in because they require an interactive desktop/X11 session:

```bash
go test -tags "record_ffmpeg record_integration" ./tools/record
```
