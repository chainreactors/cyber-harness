// Command gen is the single protobuf generation entrypoint for AIScan.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const modulePath = "github.com/chainreactors/aiscan"

var aopProtos = []string{
	"aop/value.proto",
	"aop/content.proto",
	"aop/event.proto",
	"aop/chat.proto",
	"aop/envelope.proto",
	"aop/protocol.proto",
	"aop/file/protocol.proto",
	"aop/exec/protocol.proto",
	"aop/pty/protocol.proto",
	"aop/tool/protocol.proto",
	"aop/sco/protocol.proto",
}

var typeProtos = []string{
	"aiscan/types/agent.proto",
	"aiscan/types/chat.proto",
	"aiscan/types/command.proto",
	"aiscan/types/config.proto",
	"aiscan/types/reload.proto",
	"aiscan/types/scan.proto",
	"aiscan/types/sco.proto",
	"aiscan/types/system.proto",
}

var rpcProtos = []string{
	"aiscan/rpc/agent.proto",
	"aiscan/rpc/chat.proto",
	"aiscan/rpc/config.proto",
	"aiscan/rpc/scan.proto",
	"aiscan/rpc/sco.proto",
	"aiscan/rpc/system.proto",
}

func main() {
	root, err := repositoryRoot()
	if err != nil {
		fatal("locate repository root", err)
	}
	protoc, err := exec.LookPath("protoc")
	if err != nil {
		fatal("find protoc", err)
	}
	esPlugin, err := findESPlugin(root)
	if err != nil {
		fatal("find protoc-gen-es (run npm install in web/frontend)", err)
	}

	cyberProto := filepath.Join(root, "web", "frontend", "cyber-ui", "packages", "aop", "proto")
	productProto := filepath.Join(root, "proto")
	aopTS := filepath.Join(root, "web", "frontend", "cyber-ui", "packages", "aop", "src", "gen", "aop")
	productTS := filepath.Join(root, "web", "frontend", "src", "gen", "aiscan")

	for _, path := range []string{
		filepath.Join(root, "pkg", "types", "agent"),
		filepath.Join(root, "pkg", "types", "chat"),
		filepath.Join(root, "pkg", "types", "command"),
		filepath.Join(root, "pkg", "types", "config"),
		filepath.Join(root, "pkg", "types", "reload"),
		filepath.Join(root, "pkg", "types", "scan"),
		filepath.Join(root, "pkg", "types", "sco"),
		filepath.Join(root, "pkg", "types", "system"),
		filepath.Join(root, "pkg", "rpc"),
		filepath.Join(root, "web", "frontend", "cyber-ui", "packages", "aop", "src", "gen", "aiscan"),
		aopTS,
		productTS,
	} {
		if err := os.RemoveAll(path); err != nil {
			fatal("clear generated output "+path, err)
		}
	}

	goInputs := append(append([]string{}, aopProtos...), typeProtos...)
	goInputs = append(goInputs, rpcProtos...)
	sort.Strings(goInputs)
	goArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--go_out=" + root,
		"--go_opt=module=" + modulePath,
	}
	goArgs = append(goArgs, absoluteInputs(cyberProto, productProto, goInputs)...)
	run(root, protoc, goArgs...)

	connectArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--connect-go_out=" + root,
		"--connect-go_opt=module=" + modulePath,
	}
	connectArgs = append(connectArgs, absoluteInputs(cyberProto, productProto, rpcProtos)...)
	run(root, protoc, connectArgs...)

	if err := os.MkdirAll(filepath.Dir(aopTS), 0o755); err != nil {
		fatal("create AOP TypeScript output", err)
	}
	aopArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--plugin=protoc-gen-es=" + esPlugin,
		"--es_out=" + filepath.Dir(aopTS),
		"--es_opt=target=ts,import_extension=js",
	}
	aopArgs = append(aopArgs, absoluteInputs(cyberProto, productProto, aopProtos)...)
	run(root, protoc, aopArgs...)

	if err := os.MkdirAll(filepath.Dir(productTS), 0o755); err != nil {
		fatal("create AIScan TypeScript output", err)
	}
	productInputs := append(append([]string{}, typeProtos...), rpcProtos...)
	sort.Strings(productInputs)
	productArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--plugin=protoc-gen-es=" + esPlugin,
		"--es_out=" + filepath.Join(root, "web", "frontend", "src", "gen"),
		"--es_opt=target=ts,import_extension=js",
	}
	productArgs = append(productArgs, absoluteInputs(cyberProto, productProto, productInputs)...)
	run(root, protoc, productArgs...)
	if err := rewriteProductAOPImports(productTS); err != nil {
		fatal("rewrite AIScan TypeScript AOP imports", err)
	}
}

func rewriteProductAOPImports(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".ts" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		value := string(data)
		next := strings.ReplaceAll(value, `"../../aop/`, `"../../../../cyber-ui/packages/aop/src/gen/aop/`)
		if next == value {
			return nil
		}
		return os.WriteFile(path, []byte(next), 0o644)
	})
}

func absoluteInputs(cyberProto, productProto string, inputs []string) []string {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		base := productProto
		if len(input) >= 4 && input[:4] == "aop/" {
			base = cyberProto
		}
		values = append(values, filepath.Join(base, filepath.FromSlash(input)))
	}
	return values
}

func findESPlugin(root string) (string, error) {
	name := "protoc-gen-es"
	if runtime.GOOS == "windows" {
		name += ".cmd"
	}
	local := filepath.Join(root, "web", "frontend", "node_modules", ".bin", name)
	if _, err := os.Stat(local); err == nil {
		return local, nil
	}
	return exec.LookPath("protoc-gen-es")
}

func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}

func run(dir, command string, args ...string) {
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fatal(command, err)
	}
}

func fatal(action string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", action, err)
	os.Exit(1)
}
