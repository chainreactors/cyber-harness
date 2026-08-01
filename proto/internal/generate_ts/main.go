package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func main() {
	protoc, err := exec.LookPath("protoc")
	if err != nil {
		fatal("find protoc", err)
	}
	pluginName := "protoc-gen-es"
	if runtime.GOOS == "windows" {
		pluginName += ".cmd"
	}
	plugin := filepath.Join("..", "web", "frontend", "node_modules", ".bin", pluginName)
	plugin, err = filepath.Abs(plugin)
	if err != nil {
		fatal("resolve protoc-gen-es", err)
	}
	if _, err := os.Stat(plugin); err != nil {
		if plugin, err = exec.LookPath("protoc-gen-es"); err != nil {
			fatal("find protoc-gen-es (run npm install in web/frontend)", err)
		}
	}

	args := []string{
		"-I", "../web/frontend/cyber-ui/packages/aop/proto",
		"-I", ".",
		"--plugin=protoc-gen-es=" + plugin,
		"--es_out=../web/frontend/cyber-ui/packages/aop/src/gen",
		"--es_opt=target=ts,import_extension=js",
		"../web/frontend/cyber-ui/packages/aop/proto/aop/value.proto",
		"../web/frontend/cyber-ui/packages/aop/proto/aop/content.proto",
		"../web/frontend/cyber-ui/packages/aop/proto/aop/event.proto",
		"../web/frontend/cyber-ui/packages/aop/proto/aop/chat.proto",
		"aiscan/chat/session.proto",
		"aiscan/scan/scan.proto",
		"aiscan/transport/terminal.proto",
	}
	command := exec.Command(protoc, args...)
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		fatal("generate TypeScript protobuf", err)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
