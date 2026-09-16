.DEFAULT_GOAL := standard

GO ?= go
BASH ?= $(dir $(shell command -v sh))bash
PROJECT_ROOT := $(shell pwd -W 2>/dev/null || pwd)

WEB_DIR ?= web/frontend
WEB_ADDR ?= 127.0.0.1:8080
WEB_TOKEN ?=
BIN_DIR ?= bin

# `OS` comes from the environment, which `make` can lose when it is started
# from an MSYS/Git-Bash shell. Fall back to `uname -s`; redirecting its stderr
# breaks under MSYS path rewriting.
UNAME_S := $(shell uname -s)

ifeq ($(OS),Windows_NT)
WINDOWS := 1
else ifneq ($(filter MINGW% MSYS% CYGWIN%,$(UNAME_S)),)
WINDOWS := 1
else
WINDOWS :=
endif

ifeq ($(WINDOWS),1)
EXE := .exe
NPM ?= npm.cmd
else
EXE :=
NPM ?= npm
endif

# A `make` started from an MSYS/Git-Bash shell can lose the environment
# entirely, which breaks the frontend toolchain and the Go compiler's temp
# directory. `OS` is set by Windows itself, so an empty one on Windows means
# the environment is gone; fail fast instead of failing lower down.
ifeq ($(WINDOWS),1)
ifeq ($(OS),)
$(error make lost its environment; run it from cmd.exe or PowerShell, e.g. cmd /c "make full")
endif
endif

STANDARD_BIN ?= $(BIN_DIR)/aiscan$(EXE)
FULL_BIN ?= $(BIN_DIR)/aiscan-full$(EXE)
RECORD_BIN ?= $(BIN_DIR)/aiscan-record$(EXE)
RUNNER_BIN ?= $(BIN_DIR)/runner$(EXE)

# Standard/full match release artifacts.
STANDARD_TAGS := forceposix emptytemplates noembed osusergo netgo
FULL_TAGS := forceposix emptytemplates noembed osusergo netgo full sqlite cstx re2_cgo re2_static
RECORD_TAGS := $(FULL_TAGS) record_ffmpeg
BUILD_FLAGS := -trimpath -buildvcs=false
GO_LDFLAGS ?= -s -w

ifeq ($(WINDOWS),1)
NATIVE_OS := windows
else ifeq ($(UNAME_S),Linux)
NATIVE_OS := linux
else ifeq ($(UNAME_S),Darwin)
NATIVE_OS := darwin
else
NATIVE_OS := unsupported
endif

# The native SDKs below are prebuilt and published by chainreactors/native;
# these targets only download, verify, and unpack them. The cache layout
# `.cache/native/<family>/<os>_<arch>` is shared with `.github/native/sdk.sh`,
# which is what actually performs the install.
RECORD_ARCH ?= $(shell $(GO) env GOARCH)
RE2_ARCH ?= $(shell $(GO) env GOARCH)
RE2_PREFIX := $(if $(CYBER_RE2_PREFIX),$(CYBER_RE2_PREFIX),$(PROJECT_ROOT)/.cache/native/re2/$(NATIVE_OS)_$(RE2_ARCH))
RE2_LDFLAGS := -L$(RE2_PREFIX)/lib

ifeq ($(NATIVE_OS),windows)
RECORD_PLATFORM := windows
RECORD_PKG_CONFIG := $(PROJECT_ROOT)/.github/native/pkg-config-static.cmd
RECORD_EXTRA_LDFLAGS := -static -static-libgcc
else ifeq ($(NATIVE_OS),linux)
RECORD_PLATFORM := linux
RECORD_PKG_CONFIG := $(CURDIR)/.github/native/pkg-config-static.sh
RECORD_EXTRA_LDFLAGS :=
else
RECORD_PLATFORM := unsupported
endif
RECORD_PREFIX := $(if $(CYBER_RECORD_PREFIX),$(CYBER_RECORD_PREFIX),$(PROJECT_ROOT)/.cache/native/record/$(RECORD_PLATFORM)_$(RECORD_ARCH))
# A single CGO_LDFLAGS must carry both prefixes: setting it twice in one recipe
# line would silently drop the first.
RECORD_BUILD_ENV := PKG_CONFIG="$(RECORD_PKG_CONFIG)" PKG_CONFIG_PATH="$(RECORD_PREFIX)/lib/pkgconfig" CGO_CFLAGS="-I$(RECORD_PREFIX)/include" CGO_LDFLAGS="-L$(RECORD_PREFIX)/lib $(RECORD_EXTRA_LDFLAGS) $(RE2_LDFLAGS)"

.PHONY: help prepare frontend proto-gen standard runner full record record-native re2-static web-build web-run web all clean harness harness-llm check-architecture

help:
	@echo "aiscan build targets:"
	@echo "  make / make standard  Build the standard aiscan edition"
	@echo "  make runner           Build the tag-free runner binary"
	@echo "  make full             Build frontend, then build the full edition"
	@echo "  make record           Build the record-enabled edition (supported platforms only)"
	@echo "  make web              Build the full edition and start the Web UI"
	@echo "  make frontend         Build only web/frontend into web/static"
	@echo "  make re2-static       Install the static RE2 SDK"
	@echo "  make record-native    Install the recorder SDK"
	@echo "  make proto-gen        Regenerate all AOP and Cyber protobuf bindings"
	@echo "  make harness          Run user scenarios against the real product process"
	@echo "  make harness-llm      Run real LLM scenarios (requires explicit credentials)"
	@echo "  make check-architecture  Run static repository and dependency guards"
	@echo "  make all              Build the standard and full editions"
	@echo ""
	@echo "Variables:"
	@echo "  BIN_DIR=path          Binary output directory (default: $(BIN_DIR))"
	@echo "  WEB_ADDR=host:port    Web listen address (default: $(WEB_ADDR))"
	@echo "  WEB_TOKEN=token       Optional fixed Web access token"

harness:
	$(GO) test -count=1 -v -timeout 5m ./cmd/harness/...

harness-llm:
	$(GO) test -tags live_llm -run '^TestLiveLLM' -count=1 -v -timeout 8m ./cmd/harness/...

.PHONY: harness-llm-ioa
harness-llm-ioa:
	$(GO) test -tags live_llm -run '^TestLiveLLMMultiAgentIOAThreadAndIsolation$$' -count=1 -v -timeout 5m ./cmd/harness/...

.PHONY: harness-llm-subagent
harness-llm-subagent:
	$(GO) test -tags live_llm -run '^TestLiveLLMParentDelegatesIOASiblings$$' -count=1 -v -timeout 5m ./cmd/harness/...

check-architecture:
	$(GO) test -count=1 ./core/extension ./core/registry

prepare:
	mkdir -p "$(BIN_DIR)"

proto-gen:
	$(GO) run ./cmd/gen

frontend:
	$(NPM) --prefix "$(WEB_DIR)" run build

standard: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(STANDARD_TAGS)" -o "$(STANDARD_BIN)" ./cmd/aiscan
	@echo "Built standard edition: $(STANDARD_BIN)"

runner: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -o "$(RUNNER_BIN)" ./cmd/runner
	@echo "Built runner: $(RUNNER_BIN)"

# Full and record-enabled binaries embed web/static, so frontend must finish first.
record-native:
ifeq ($(RECORD_PLATFORM),unsupported)
	@echo "record native backend is not supported on this platform" >&2
	@exit 1
else
	"$(BASH)" ".github/native/sdk.sh" fetch record "$(RECORD_PLATFORM)" "$(RECORD_ARCH)"
endif

re2-static:
ifeq ($(NATIVE_OS),unsupported)
	@echo "static RE2 SDK is not available for this platform" >&2
	@exit 1
else
	"$(BASH)" ".github/native/sdk.sh" fetch re2 "$(NATIVE_OS)" "$(RE2_ARCH)"
endif

# The full edition links the static RE2 SDK, so the fetch is part of the build
# rather than a step the caller has to remember.
full: frontend re2-static prepare
	CGO_ENABLED=1 CGO_LDFLAGS="$(RE2_LDFLAGS)" $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(FULL_TAGS)" -o "$(FULL_BIN)" ./cmd/aiscan
	@echo "Built full edition: $(FULL_BIN)"

ifeq ($(RECORD_PLATFORM),unsupported)
record:
	@echo "record native backend is not supported on this platform" >&2
	@exit 1
else
# `record-native` and `re2-static` install both SDKs, so `make record` needs no
# pre-step and links both static RE2 and the recorder backend.
record: frontend record-native re2-static prepare
	$(RECORD_BUILD_ENV) CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(RECORD_TAGS)" -o "$(RECORD_BIN)" ./cmd/aiscan
	@echo "Built record-enabled edition: $(RECORD_BIN)"
endif

web-build: full

web-run:
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

web: full
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

all: standard runner full

clean:
	rm -f "$(STANDARD_BIN)" "$(FULL_BIN)" "$(RECORD_BIN)" "$(RUNNER_BIN)"
