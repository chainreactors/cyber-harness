.DEFAULT_GOAL := standard

GO ?= go

WEB_DIR ?= web/frontend
WEB_ADDR ?= 127.0.0.1:8080
WEB_TOKEN ?=
BIN_DIR ?= bin

RE2_TAGS := re2_cgo re2_static

ifeq ($(OS),Windows_NT)
EXE := .exe
NPM ?= npm.cmd
else
EXE :=
NPM ?= npm
endif

STANDARD_BIN ?= $(BIN_DIR)/aiscan$(EXE)
AGENT_BIN ?= $(BIN_DIR)/aiscan-agent$(EXE)
FULL_BIN ?= $(BIN_DIR)/aiscan-full$(EXE)

# Keep the local feature tiers aligned with the release workflow.
STANDARD_TAGS := forceposix emptytemplates noembed osusergo netgo cstx_native $(RE2_TAGS)
AGENT_TAGS := forceposix emptytemplates noembed osusergo netgo
FULL_TAGS := forceposix emptytemplates noembed osusergo netgo full cstx_native katana_slim $(RE2_TAGS)
BUILD_FLAGS := -trimpath -buildvcs=false

.PHONY: help prepare frontend standard agent full web-build web-run web all clean

help:
	@echo "AIScan build targets (aligned with release editions):"
	@echo "  make / make standard  Build the standard AIScan edition"
	@echo "  make agent            Build the lightweight agent edition"
	@echo "  make full             Build frontend, then build the full edition"
	@echo "  make web              Build the full edition and start the Web UI"
	@echo "  make frontend         Build only web/frontend into web/static"
	@echo "  make all              Build all three editions"
	@echo ""
	@echo "Variables:"
	@echo "  BIN_DIR=path          Binary output directory (default: $(BIN_DIR))"
	@echo "  WEB_ADDR=host:port    Web listen address (default: $(WEB_ADDR))"
	@echo "  WEB_TOKEN=token       Optional fixed Web access token"

prepare:
	mkdir -p "$(BIN_DIR)"

frontend:
	$(NPM) --prefix "$(WEB_DIR)" run build

standard: prepare
	CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(CGO_LDFLAGS)" -tags "$(STANDARD_TAGS)" -o "$(STANDARD_BIN)" ./cmd/aiscan
	@echo "Built standard edition: $(STANDARD_BIN)"

agent: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "-s -w" -tags "$(AGENT_TAGS)" -o "$(AGENT_BIN)" ./cmd/agent
	@echo "Built agent edition: $(AGENT_BIN)"

# The full binary embeds web/static, so frontend must finish first.
full: frontend prepare
	CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(CGO_LDFLAGS)" -tags "$(FULL_TAGS)" -o "$(FULL_BIN)" ./cmd/aiscan
	@echo "Built full edition: $(FULL_BIN)"

web-build: full

web-run:
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

web: full
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

all: standard agent full

clean:
	rm -f "$(STANDARD_BIN)" "$(AGENT_BIN)" "$(FULL_BIN)"
