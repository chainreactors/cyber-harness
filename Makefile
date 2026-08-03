.DEFAULT_GOAL := standard

GO ?= go

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

# Standard/full match release artifacts.
STANDARD_TAGS := forceposix emptytemplates noembed osusergo netgo
FULL_TAGS := forceposix emptytemplates noembed osusergo netgo full sqlite
BUILD_FLAGS := -trimpath -buildvcs=false

.PHONY: help prepare frontend proto-gen aop-gen standard full web-build web-run web all clean

help:
	@echo "AIScan build targets:"
	@echo "  make / make standard  Build the standard AIScan edition"
	@echo "  make full             Build frontend, then build the full edition"
	@echo "  make web              Build the full edition and start the Web UI"
	@echo "  make frontend         Build only web/frontend into web/static"
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

# Compatibility alias; AOP and product schemas share one generation entrypoint.
aop-gen: proto-gen

frontend:
	$(NPM) --prefix "$(WEB_DIR)" run build

standard: prepare
	CGO_ENABLED=0 $(GO) build $(BUILD_FLAGS) -ldflags "$(CGO_LDFLAGS)" -tags "$(STANDARD_TAGS)" -o "$(STANDARD_BIN)" ./cmd/aiscan
	@echo "Built standard edition: $(STANDARD_BIN)"

# The full binary embeds web/static, so frontend must finish first.
full: frontend prepare
	CGO_ENABLED=1 $(GO) build $(BUILD_FLAGS) -ldflags "$(CGO_LDFLAGS)" -tags "$(FULL_TAGS)" -o "$(FULL_BIN)" ./cmd/aiscan
	@echo "Built full edition: $(FULL_BIN)"

web-build: full

web-run:
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

web: full
	"$(FULL_BIN)" web --addr "$(WEB_ADDR)" $(if $(strip $(WEB_TOKEN)),--token "$(WEB_TOKEN)",)

all: standard full

clean:
	rm -f "$(STANDARD_BIN)" "$(FULL_BIN)"
