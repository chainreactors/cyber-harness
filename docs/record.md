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
- Outputs are written below `.aiscan/record/` unless `output` is specified.
- At most four recordings run concurrently. Set `AISCAN_RECORD_MAX_CONCURRENT` to a value from 1 to 16 to change the limit.

Media transport uses the existing AOP media and file namespaces. Screenshot
previews are returned as bounded inline `Content.media` data. Completed videos
are returned as `Content.media` with a task-relative `Resource.uri`; consumers
read the underlying MP4 through chunked `aop.file` requests. When a tool
invocation supplies a work directory, the default output is
`<workdir>/.aiscan/record/`, so remote runners can expose the URI without
leaking or depending on a machine-global data path.

Limitations:

- Video only; microphone and system audio are not captured.
- Wayland is not supported. Use an X11 session.
- macOS and Windows arm64 do not have a native recorder backend.
- Capture requires an interactive graphical session; headless hosts and Windows session 0 are not supported.
- The window must be visible and non-minimized. Capture size is fixed when recording starts; closing, minimizing, or shrinking the window can terminate the recording.
- The native backend is not present in official full builds. Custom builds require CGO, the `record_ffmpeg` build tag, and a supported C toolchain.

Record-enabled builds statically link a feature-minimal FFmpeg and x264, so users do not install either runtime separately. This is single-file distribution, not literally zero runtime dependencies: Windows still uses system DLLs; Linux requires glibc, X11/XCB libraries, and an accessible `DISPLAY`. The SDK only enables the platform capture input, its raw/BMP decoder, libx264, the MP4 muxer, file output, and pixel conversion. It is not a general-purpose FFmpeg build.

## Two-stage native build

Build the record-enabled edition with the dedicated target:

```bash
make record
```

`make record` builds the frontend, downloads a versioned SDK into `.cache/record-native/<platform>-<arch>`, verifies its SHA-256 sidecar and manifest, applies the native link environment, and compiles `bin/aiscan-record` with the `full` and `record_ffmpeg` tags. Supported SDK targets are Linux amd64/arm64 and Windows amd64. Linux source builds still need a C compiler, `pkg-config`, and XCB development packages; Windows source builds need MinGW-w64 and `pkgconf`.

Maintainers build the SDK from the pinned commits separately:

```bash
make record-native-source record-native-package
```

Set `RECORD_ARCH=arm64` or `RECORD_NATIVE_OUTPUT=<directory>` when the defaults do not match the target. The Makefile is the supported build interface; `.github/native/sdk.sh` is the underlying maintainer/CI implementation.

The `recorder-native-sdk` GitHub Actions workflow performs that source-build/package phase for every supported target and publishes the archives under the release tag declared in `.github/native/versions.env`. It is independent of the normal CI and release build paths. Set `AISCAN_RECORD_BUILD_FROM_SOURCE=1` when invoking `make record` or `make record-native` to opt into the slow source-build path locally. `AISCAN_RECORD_PREFIX` changes the SDK cache/install directory, and `AISCAN_RECORD_NATIVE_URL` can point downloads at an internal mirror.

The source build verifies an exact FFmpeg component allowlist, and packaging rejects static libraries above a 16 MiB budget unless `AISCAN_RECORD_MAX_LIB_BYTES` explicitly overrides it. This prevents an FFmpeg upgrade or configure change from silently restoring all default codecs and adding tens of megabytes to record-enabled binaries.

Native smoke tests are opt-in because they require an interactive desktop/X11 session:

```bash
go test -tags "record_ffmpeg record_integration" ./tools/record
```
