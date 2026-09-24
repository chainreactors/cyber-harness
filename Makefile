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
AGENT_BIN ?= $(BIN_DIR)/agent$(EXE)
AUDIT_BIN ?= $(BIN_DIR)/cyber-audit$(EXE)

# The edition tag sets live in editions.env, which the CI workflows read too.
include editions.env

BUILD_FLAGS := -trimpath -buildvcs=false
GO_LDFLAGS ?= -s -w

# `emptytemplates`/`noembed` keep the resource templates outside the binary.
# EMBED=1 drops both and generates them into the tree first, which is the only
# difference between an embedded and a non-embedded build.
RESOURCE_TAGS := emptytemplates noembed
EMBED ?= 0

ifeq ($(EMBED),1)
EMBED_PREREQ := embed-resources
STANDARD_TAGS := $(filter-out $(RESOURCE_TAGS),$(STANDARD_TAGS))
FULL_TAGS := $(filter-out $(RESOURCE_TAGS),$(FULL_TAGS))
RECORD_TAGS := $(filter-out $(RESOURCE_TAGS),$(RECORD_TAGS))
else
EMBED_PREREQ :=
endif

# Tool payloads are independent of scanner template embedding.
ARSENAL_EMBED ?= 0
ARSENAL_CONFIG ?= cmd/aiscan/bundle.yaml
AUDIT_ARSENAL_CONFIG ?= audit/cmd/cyber-audit/bundle.yaml
ARSENAL_GOOS := $(shell $(GO) env GOOS)
ARSENAL_GOARCH := $(shell $(GO) env GOARCH)
ifeq ($(ARSENAL_EMBED),1)
EMBED_PREREQ += embed-arsenal
STANDARD_TAGS += arsenal_embed
FULL_TAGS += arsenal_embed
RECORD_TAGS += arsenal_embed
AUDIT_PREREQ := embed-audit
AUDIT_TAGS := arsenal_embed
else
EMBED_PREREQ += arsenal-spec
AUDIT_PREREQ := audit-arsenal-spec
endif

ifeq ($(WINDOWS),1)
NATIVE_OS := windows
else ifeq ($(UNAME_S),Linux)
NATIVE_OS := linux
else ifeq ($(UNAME_S),Darwin)
NATIVE_OS := darwin
else
NATIVE_OS := unsupported
endif

# The recorder SDK is prebuilt and published by chainreactors/native. This
# target only downloads, verifies, and unpacks it through .github/native/sdk.sh.
RECORD_ARCH ?= $(shell $(GO) env GOARCH)

ifeq ($(NATIVE_OS),windows)
RECORD_PLATFORM := windows
RECORD_EXTRA_LDFLAGS := -static -static-libgcc
else ifeq ($(NATIVE_OS),linux)
RECORD_PLATFORM := linux
RECORD_EXTRA_LDFLAGS :=
else
RECORD_PLATFORM := unsupported
endif
RECORD_PREFIX := $(if $(CYBER_RECORD_PREFIX),$(CYBER_RECORD_PREFIX),$(PROJECT_ROOT)/.cache/native/record/$(RECORD_PLATFORM)_$(RECORD_ARCH))
RECORD_BUILD_ENV := CGO_LDFLAGS="-L$(RECORD_PREFIX)/lib $(RECORD_EXTRA_LDFLAGS)"

.PHONY: help prepare frontend proto-gen standard agent full record record-native web-build web-run web all clean harness harness-llm check-architecture embed-resources ldflags

help:
	@echo "aiscan build targets:"
	@echo "  make / make standard  Build the standard aiscan edition"
	@echo "  make agent            Build the minimal local agent binary"
	@echo "  make audit ARSENAL_EMBED=1  Build audit with all required external tools"
	@echo "  make full             Build frontend, then build the full edition"
	@echo "  make record           Build the record-enabled edition (supported platforms only)"
	@echo "  make web              Build the full edition and start the Web UI"
	@echo "  make frontend         Build only web/frontend into web/static"
	@echo "  make embed-arsenal    Download tools and generate target-specific embedding"
	@echo "  make record-native    Install the recorder SDK"
	@echo "  make proto-gen        Regenerate all AOP and Cyber protobuf bindings"
	@echo "  make harness          Run user scenarios against the real application process"
	@echo "  make harness-llm      Run real LLM scenarios (requires explicit credentials)"
	@echo "  make check-architecture  Run static repository and dependency guards"
	@echo "  make ldflags          Print the -ldflags the build targets use"
	@echo "  make all              Build the standard and full editions"
	@echo ""
	@echo "Variables:"
	@echo "  EMBED=1               Generate and embed resources instead of loading them"
	@echo "  ARSENAL_EMBED=1       Embed tools selected by ARSENAL_CONFIG"
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
	$(GO) test -count=1 ./core/extension ./core/registry ./pkg/exts

prepare:
	mkdir -p "$(BIN_DIR)"

# Only reachable through EMBED=1, which also strips the tags that would
# otherwise keep these resources external.
embed-resources:
	$(GO) generate ./tools/resources ./tools/proton/resources

.PHONY: embed-arsenal arsenal-spec
embed-arsenal arsenal-spec:
	GOOS=$(shell $(GO) env GOHOSTOS) GOARCH=$(shell $(GO) env GOHOSTARCH) $(GO) run github.com/chainreactors/crtm/cmd/crtm-bundle -config "$(ARSENAL_CONFIG)" -target "$(ARSENAL_GOOS)/$(ARSENAL_GOARCH)" -output cmd/aiscan -package main $(if $(filter arsenal-spec,$@),-metadata-only)

.PHONY: audit embed-audit audit-arsenal-spec
embed-audit audit-arsenal-spec:
	GOOS=$(shell $(GO) env GOHOSTOS) GOARCH=$(shell $(GO) env GOHOSTARCH) $(GO) run github.com/chainreactors/crtm/cmd/crtm-bundle -config "$(AUDIT_ARSENAL_CONFIG)" -target "$(ARSENAL_GOOS)/$(ARSENAL_GOARCH)" -output audit/internal/toolchain -package toolchain $(if $(filter audit-arsenal-spec,$@),-metadata-only)

audit: $(AUDIT_PREREQ) prepare
	CGO_ENABLED=0 GOWORK=off $(GO) -C audit build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(AUDIT_TAGS)" -o "$(abspath $(AUDIT_BIN))" ./cmd/cyber-audit
	@echo "Built audit: $(AUDIT_BIN)"

ldflags:
	@echo "$(GO_LDFLAGS)"

proto-gen:
	$(GO) run ./cmd/gen

frontend:
	$(NPM) --prefix "$(WEB_DIR)" run build

standard: $(EMBED_PREREQ) prepare
	CGO_ENABLED=$(STANDARD_CGO) $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(STANDARD_TAGS)" -o "$(STANDARD_BIN)" ./cmd/aiscan
	@echo "Built standard edition: $(STANDARD_BIN)"

agent: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -o "$(AGENT_BIN)" ./cmd/agent
	@echo "Built minimal agent: $(AGENT_BIN)"

# Full and record-enabled binaries embed web/static, so frontend must finish first.
record-native:
ifeq ($(RECORD_PLATFORM),unsupported)
	@echo "record native backend is not supported on this platform" >&2
	@exit 1
else
	"$(BASH)" ".github/native/sdk.sh" fetch record "$(RECORD_PLATFORM)" "$(RECORD_ARCH)"
endif

full: $(EMBED_PREREQ) frontend prepare
	CGO_ENABLED=$(FULL_CGO) $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(FULL_TAGS)" -o "$(FULL_BIN)" ./cmd/aiscan
	@echo "Built full edition: $(FULL_BIN)"

ifeq ($(RECORD_PLATFORM),unsupported)
record:
	@echo "record native backend is not supported on this platform" >&2
	@exit 1
else
# `record-native` installs the only SDK this edition needs.
record: $(EMBED_PREREQ) frontend record-native prepare
	$(RECORD_BUILD_ENV) CGO_ENABLED=$(RECORD_CGO) $(GO) build $(BUILD_FLAGS) -ldflags "$(GO_LDFLAGS)" -tags "$(RECORD_TAGS)" -o "$(RECORD_BIN)" ./cmd/aiscan
	@echo "Built record-enabled edition: $(RECORD_BIN)"
endif

web-build: full

web-run:
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

web: full
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

all: standard agent full

clean:
	rm -f "$(STANDARD_BIN)" "$(FULL_BIN)" "$(RECORD_BIN)" "$(AGENT_BIN)"
