# aiscan build targets.

GO      ?= go
WASM_DIR ?= dist/wasm
WASM_OUT ?= $(WASM_DIR)/agent.wasm

.PHONY: agent-wasm agent-wasm-size clean-wasm

## agent-wasm: build the agent-core js/wasm module (RFC #189, 方案 A) + wasm_exec.js
agent-wasm:
	@mkdir -p $(WASM_DIR)
	GOOS=js GOARCH=wasm $(GO) build -trimpath -ldflags="-s -w" -o $(WASM_OUT) ./cmd/wasm
	@cp "$$($(GO) env GOROOT)/lib/wasm/wasm_exec.js" $(WASM_DIR)/wasm_exec.js
	@gzip -9 -c $(WASM_OUT) > $(WASM_OUT).gz
	@$(MAKE) --no-print-directory agent-wasm-size

## agent-wasm-size: print raw and gzipped module size (the GATE metric)
agent-wasm-size:
	@echo "── agent.wasm size ──"
	@ls -lh $(WASM_OUT) $(WASM_OUT).gz 2>/dev/null | awk '{printf "  %-6s %s\n", $$5, $$9}'

clean-wasm:
	@rm -rf $(WASM_DIR)
