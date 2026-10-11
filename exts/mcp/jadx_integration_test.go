package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	coretool "github.com/chainreactors/cyber/core/tool"
	mcptools "github.com/chainreactors/cyber/tools/mcp"
	"mvdan.cc/sh/v3/syntax"
)

// This opt-in lane runs REA's audited, unmodified open-source JADX engine.
// It neither downloads tools nor uses a simulated MCP server or decompiler.
func TestJADXRealBashWorkflow(t *testing.T) {
	jar, apk := os.Getenv("CYBER_MCP_JADX_JAR"), os.Getenv("CYBER_MCP_JADX_APK")
	if jar == "" && apk == "" {
		t.Skip("real JADX requires CYBER_MCP_JADX_JAR and CYBER_MCP_JADX_APK; see docs/mcp.md")
	}
	if jar == "" || apk == "" || !filepath.IsAbs(jar) || !filepath.IsAbs(apk) {
		t.Fatal("provide absolute paths to both JADX 0.7.1 and ApiDemos v6.0.18 debug APK")
	}
	for path, digest := range map[string]string{
		jar: "6e5eacf500b64292bfb73c49797c1958f6ee44646e43e868039ae7feb573ff75",
		apk: "a9eecf37b26cd084855c530db81c2bb1b91f4c1b095a04f47aa7c20e2791f686",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		actual := sha256.Sum256(data)
		if hex.EncodeToString(actual[:]) != digest {
			t.Fatalf("artifact %s does not match the audited fixture SHA-256", path)
		}
		t.Logf("verified artifact %s sha256=%s", filepath.Base(path), digest)
	}
	java := os.Getenv("CYBER_MCP_JAVA")
	if java == "" {
		java = "java"
	}
	h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{
		"jadx": {Command: java, Args: []string{"-Xmx512m", "-jar", jar}, TimeoutSeconds: 90},
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := h.set.Load(ctx); err != nil {
		t.Fatal(err)
	}
	// Exercise caller paths containing spaces through real shell parsing.
	input, err := os.ReadFile(apk)
	if err != nil {
		t.Fatal(err)
	}
	selectedAPK := filepath.Join(h.dir, "ApiDemos with spaces.apk")
	if err := os.WriteFile(selectedAPK, input, 0600); err != nil {
		t.Fatal(err)
	}
	quotedAPK, err := syntax.Quote(selectedAPK, syntax.LangBash)
	if err != nil {
		t.Fatal(err)
	}
	// Use the actual AI tool boundary and inspect its unabridged redirected output.
	run := func(command string, fails bool) []byte {
		t.Helper()
		arguments, err := json.Marshal(map[string]any{"command": command + " > result.json 2> diagnostics.txt", "timeout": 90})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		result, err := h.tools.ExecuteTool(ctx, "bash", string(arguments))
		if err != nil || result == nil || result.IsError != fails {
			diagnostics, _ := os.ReadFile(filepath.Join(h.dir, "diagnostics.txt"))
			t.Fatalf("bash command %q: %v %s %s", command, err, coretool.ResultText(result), diagnostics)
		}
		output, err := os.ReadFile(filepath.Join(h.dir, "result.json"))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("bash %s: expected_failure=%t bytes=%d elapsed=%s", command, fails, len(output), time.Since(started).Round(time.Millisecond))
		return output
	}
	text := func(raw []byte) string {
		t.Helper()
		var result struct {
			Content []struct{ Type, Text string } `json:"content"`
		}
		if err := json.Unmarshal(raw, &result); err != nil || len(result.Content) == 0 || result.Content[0].Type != "text" {
			t.Fatalf("invalid real MCP result: %s %v", raw, err)
		}
		return result.Content[0].Text
	}
	var tools []struct{ Name string }
	if err := json.Unmarshal(run("jadx --list", false), &tools); err != nil || len(tools) < 20 {
		t.Fatalf("real catalog missing tools: %v %v", tools, err)
	}
	t.Logf("discovered %d real JADX tools", len(tools))
	help := string(run("jadx get_class_source --help", false))
	for _, flag := range []string{"--class_name <string> (required)", "--max_bytes <integer>", "--smali_fallback <boolean>", `"inputSchema"`} {
		if !strings.Contains(help, flag) {
			t.Fatalf("live schema did not generate %q: %s", flag, help)
		}
	}
	if initial := text(run("jadx status", false)); !strings.Contains(initial, "EMPTY") {
		t.Fatalf("unexpected initial engine state: %s", initial)
	}
	failed := run("jadx get_class_source --class_name io.appium.android.apis.ApiDemos", true)
	var failure struct{ IsError bool }
	if json.Unmarshal(failed, &failure) != nil || !failure.IsError {
		t.Fatalf("upstream failure lost its raw result or shell status: %s", failed)
	}
	loaded := text(run("jadx load_apk --path "+quotedAPK+" --threads 1 --resources lite", false))
	var state struct {
		State      string `json:"state"`
		ClassCount int    `json:"class_count"`
		Threads    int    `json:"threads"`
	}
	if json.Unmarshal([]byte(loaded), &state) != nil || state.State != "LOADED" || state.ClassCount == 0 || state.Threads != 1 {
		t.Fatalf("APK indexing or integer mapping failed: %s", loaded)
	}
	t.Logf("indexed %d top-level classes", state.ClassCount)
	info := text(run("jadx get_app_info", false))
	if !strings.Contains(info, "io.appium.android.apis") {
		t.Fatalf("wrong public APK analyzed: %s", info)
	}
	page := text(run("jadx list_classes --prefix io.appium.android.apis --offset 0 --limit 3", false))
	var classes struct {
		Limit int
		Items []string
	}
	if json.Unmarshal([]byte(page), &classes) != nil || classes.Limit != 3 || len(classes.Items) != 3 {
		t.Fatalf("integer pagination flags failed: %s", page)
	}
	source := text(run("class=io.appium.android.apis.ApiDemos; jadx get_class_source --class_name \"$class\" --max_bytes 32768 --smali_fallback false | { read -r result; printf '%s\\n' \"$result\"; }", false))
	for _, expected := range []string{"package io.appium.android.apis;", "class ApiDemos", "void onCreate(", "setListAdapter("} {
		if !strings.Contains(source, expected) {
			t.Fatalf("actual Java decompilation missing %q: %s", expected, source)
		}
	}
	rawSource := text(run(`jadx get_class_source --json '{"class_name":"io.appium.android.apis.ApiDemos","max_bytes":32768,"smali_fallback":false}'`, false))
	if rawSource != source {
		t.Fatal("named scalar flags and raw JSON returned different Java source")
	}
	method := text(run("jadx get_method_by_name --class_name io.appium.android.apis.ApiDemos --method_name onCreate --smali_fallback false", false))
	if !strings.Contains(method, "void onCreate(") || !strings.Contains(method, "setListAdapter(") {
		t.Fatalf("method decompilation failed: %s", method)
	}
	if unloaded := text(run("jadx unload_apk", false)); !strings.Contains(unloaded, "EMPTY") {
		t.Fatalf("upstream resource was not unloaded: %s", unloaded)
	}
	if final := text(run("jadx status", false)); !strings.Contains(final, "EMPTY") {
		t.Fatalf("upstream session state was not preserved: %s", final)
	}
	if err := h.set.Close(ctx); err != nil || h.commands.Has("jadx") {
		t.Fatalf("extension did not finish cleanup and revoke the command: %v", err)
	}
}
