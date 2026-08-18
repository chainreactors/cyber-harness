.DEFAULT_GOAL := standard

GO ?= go
BASH ?= $(dir $(shell command -v sh))bash
PROJECT_ROOT := $(shell pwd -W 2>/dev/null || pwd)

WEB_DIR ?= web/frontend
WEB_ADDR ?= 127.0.0.1:8080
WEB_TOKEN ?=
BIN_DIR ?= bin

ifeq ($(OS),Windows_NT)
EXE := .exe
NPM ?= npm.cmd
else
EXE :=
NPM ?= npm
endif

STANDARD_BIN ?= $(BIN_DIR)/aiscan$(EXE)
FULL_BIN ?= $(BIN_DIR)/aiscan-full$(EXE)
RECORD_BIN ?= $(BIN_DIR)/aiscan-record$(EXE)

# Standard/full match release artifacts.
STANDARD_TAGS := forceposix emptytemplates noembed osusergo netgo
FULL_TAGS := forceposix emptytemplates noembed osusergo netgo full sqlite re2_cgo re2_static
RECORD_TAGS := $(FULL_TAGS) record_ffmpeg
BUILD_FLAGS := -trimpath -buildvcs=false
GO_LDFLAGS ?= -s -w

UNAME_S := $(shell uname -s 2>/dev/null)
ifeq ($(OS),Windows_NT)
RECORD_PLATFORM := windows
else ifneq ($(filter MINGW% MSYS% CYGWIN%,$(UNAME_S)),)
RECORD_PLATFORM := windows
else ifeq ($(UNAME_S),Linux)
RECORD_PLATFORM := linux
else
RECORD_PLATFORM := unsupported
endif
RECORD_ARCH ?= $(shell $(GO) env GOARCH)
RECORD_NATIVE_OUTPUT ?= dist/native
RECORD_PREFIX := $(if $(AISCAN_RECORD_PREFIX),$(AISCAN_RECORD_PREFIX),$(PROJECT_ROOT)/.cache/record-native/$(RECORD_PLATFORM)-$(RECORD_ARCH))
ifeq ($(RECORD_PLATFORM),windows)
RECORD_PKG_CONFIG := $(PROJECT_ROOT)/.github/native/pkg-config-static.cmd
RECORD_EXTRA_LDFLAGS := -static -static-libgcc
else
RECORD_PKG_CONFIG := $(CURDIR)/.github/native/pkg-config-static.sh
RECORD_EXTRA_LDFLAGS :=
endif
RECORD_BUILD_ENV := PKG_CONFIG="$(RECORD_PKG_CONFIG)" PKG_CONFIG_PATH="$(RECORD_PREFIX)/lib/pkgconfig" CGO_CFLAGS="-I$(RECORD_PREFIX)/include" CGO_LDFLAGS="-L$(RECORD_PREFIX)/lib $(RECORD_EXTRA_LDFLAGS)"

.PHONY: help prepare frontend proto-gen standard full record record-native record-native-source record-native-package web-build web-run web all clean

help:
	@echo "AIScan build targets:"
	@echo "  make / make standard  Build the standard AIScan edition"
	@echo "  make full             Build frontend, then build the full edition"
	@echo "  make record           Build the record-enabled edition (supported platforms only)"
	@echo "  make web              Build the full edition and start the Web UI"
	@echo "  make frontend         Build only web/frontend into web/static"
	@echo "  make record-native    Download the prebuilt FFmpeg/x264 recorder SDK"
	@echo "  make record-native-source  Build the recorder SDK from pinned sources"
	@echo "  make record-native-package Package a source-built recorder SDK"
	@echo "  make proto-gen        Regenerate all AOP and AIScan protobuf bindings"
	@echo "  make all              Build the standard and full editions"
	@echo ""
	@echo "Variables:"
	@echo "  BIN_DIR=path          Binary output directory (default: $(BIN_DIR))"
	@echo "  WEB_ADDR=host:port    Web listen address (default: $(WEB_ADDR))"
	@echo "  WEB_TOKEN=token       Optional fixed Web access token"

prepare:
	mkdir -p "$(BIN_DIR)"

proto-gen:
	$(GO) run ./cmd/gen

frontend:
	$(NPM) --prefix "$(WEB_DIR)" run build

standard: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(STANDARD_TAGS)" -o "$(STANDARD_BIN)" ./cmd/aiscan
	@echo "Built standard edition: $(STANDARD_BIN)"

# Full and record-enabled binaries embed web/static, so frontend must finish first.
record-native:
ifeq ($(RECORD_PLATFORM),unsupported)
	@echo "record native backend is not supported on this platform"
else
	@if [ "$(AISCAN_RECORD_BUILD_FROM_SOURCE)" = "1" ]; then \
		"$(BASH)" ".github/native/sdk.sh" build "$(RECORD_PLATFORM)" "$(RECORD_ARCH)"; \
	else \
		"$(BASH)" ".github/native/sdk.sh" fetch "$(RECORD_PLATFORM)" "$(RECORD_ARCH)"; \
	fi
endif

record-native-source:
ifeq ($(RECORD_PLATFORM),unsupported)
	@echo "record native backend is not supported on this platform"
else
	"$(BASH)" ".github/native/sdk.sh" build "$(RECORD_PLATFORM)" "$(RECORD_ARCH)"
endif

record-native-package:
ifeq ($(RECORD_PLATFORM),unsupported)
	@echo "record native backend is not supported on this platform"
else
	"$(BASH)" ".github/native/sdk.sh" package "$(RECORD_PLATFORM)" "$(RECORD_ARCH)" "$(RECORD_NATIVE_OUTPUT)"
endif

full: frontend prepare
	CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(FULL_TAGS)" -o "$(FULL_BIN)" ./cmd/aiscan
	@echo "Built full edition: $(FULL_BIN)"

ifeq ($(RECORD_PLATFORM),unsupported)
record:
	@echo "record native backend is not supported on this platform" >&2
	@exit 1
else
record: frontend record-native prepare
	$(RECORD_BUILD_ENV) CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(RECORD_TAGS)" -o "$(RECORD_BIN)" ./cmd/aiscan
	@echo "Built record-enabled edition: $(RECORD_BIN)"
endif

web-build: full

web-run:
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

web: full
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

all: standard full

clean:
	rm -f "$(STANDARD_BIN)" "$(FULL_BIN)" "$(RECORD_BIN)"
