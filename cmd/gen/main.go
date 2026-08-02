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

const (
	protocVersion           = "35.1"
	protocGenGoVersion      = "v1.36.11"
	protocGenConnectVersion = "1.20.0"
	protocGenESVersion      = "v2.13.0"
)

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
	"types/agent.proto",
	"types/chat.proto",
	"types/command.proto",
	"types/config.proto",
	"types/reload.proto",
	"types/scan.proto",
	"types/sco.proto",
	"types/system.proto",
}

var rpcProtos = []string{
	"rpc/aop.proto",
	"rpc/agent.proto",
	"rpc/chat.proto",
	"rpc/config.proto",
	"rpc/scan.proto",
	"rpc/sco.proto",
	"rpc/system.proto",
}

func main() {
	root, err := repositoryRoot()
	if err != nil {
		fatal("locate repository root", err)
	}
	protoc, err := findTool(root, "PROTOC", "protoc", filepath.Join("bin", "protoc", "bin"))
	if err != nil {
		fatal("find protoc", err)
	}
	goPlugin, err := findGoTool(root, "PROTOC_GEN_GO", "protoc-gen-go")
	if err != nil {
		fatal("find protoc-gen-go", err)
	}
	connectPlugin, err := findGoTool(root, "PROTOC_GEN_CONNECT_GO", "protoc-gen-connect-go")
	if err != nil {
		fatal("find protoc-gen-connect-go", err)
	}
	esPlugin, err := findESPlugin(root)
	if err != nil {
		fatal("find protoc-gen-es (run npm install in web/frontend)", err)
	}
	checkVersion(protoc, "protoc", protocVersion)
	checkVersion(goPlugin, "protoc-gen-go", protocGenGoVersion)
	checkVersion(connectPlugin, "protoc-gen-connect-go", protocGenConnectVersion)
	checkVersion(esPlugin, "protoc-gen-es", protocGenESVersion)

	cyberProto := filepath.Join(root, "web", "frontend", "cyber-ui", "packages", "aop", "proto")
	productProto := filepath.Join(root, "proto")
	aopTS := filepath.Join(root, "web", "frontend", "cyber-ui", "packages", "aop", "src", "gen", "aop")
	productTS := filepath.Join(root, "web", "frontend", "src", "gen")
	typesDir := filepath.Join(root, "pkg", "types")

	for _, path := range []string{
		filepath.Join(root, "pkg", "rpc"),
		filepath.Join(root, "web", "frontend", "src", "gen", "rpc"),
		filepath.Join(root, "web", "frontend", "src", "gen", "types"),
		aopTS,
	} {
		if err := os.RemoveAll(path); err != nil {
			fatal("clear generated output "+path, err)
		}
	}
	if err := removeGeneratedFiles(typesDir, ".pb.go"); err != nil {
		fatal("clear generated AIScan types", err)
	}

	goInputs := append(append([]string{}, aopProtos...), typeProtos...)
	goInputs = append(goInputs, rpcProtos...)
	sort.Strings(goInputs)
	goArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--plugin=protoc-gen-go=" + goPlugin,
		"--go_out=" + root,
		"--go_opt=module=" + modulePath,
	}
	goArgs = append(goArgs, absoluteInputs(cyberProto, productProto, goInputs)...)
	run(root, protoc, goArgs...)

	connectArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--plugin=protoc-gen-connect-go=" + connectPlugin,
		"--connect-go_out=" + root,
		"--connect-go_opt=module=" + modulePath,
		"--connect-go_opt=package_suffix",
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

	if err := os.MkdirAll(productTS, 0o755); err != nil {
		fatal("create AIScan TypeScript output", err)
	}
	productInputs := append(append([]string{}, typeProtos...), rpcProtos...)
	sort.Strings(productInputs)
	productArgs := []string{
		"-I", cyberProto,
		"-I", productProto,
		"--plugin=protoc-gen-es=" + esPlugin,
		"--es_out=" + productTS,
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
		next := strings.ReplaceAll(value, `"../aop/`, `"../../../cyber-ui/packages/aop/src/gen/aop/`)
		next = strings.TrimRight(next, "\r\n") + "\n"
		if next == value {
			return nil
		}
		return os.WriteFile(path, []byte(next), 0o644)
	})
}

func removeGeneratedFiles(dir, suffix string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
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
	if value := strings.TrimSpace(os.Getenv("PROTOC_GEN_ES")); value != "" {
		return filepath.Abs(value)
	}
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

func findTool(root, envName, name, localDir string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return filepath.Abs(value)
	}
	executable := name
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if localDir != "" {
		local := filepath.Join(root, localDir, executable)
		if _, err := os.Stat(local); err == nil {
			return local, nil
		}
	}
	return exec.LookPath(name)
}

func findGoTool(root, envName, name string) (string, error) {
	if value := strings.TrimSpace(os.Getenv(envName)); value != "" {
		return filepath.Abs(value)
	}
	cmd := exec.Command("go", "tool", "-n", name)
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go tool -n %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	path := strings.Trim(strings.TrimSpace(string(output)), `"`)
	if path == "" {
		return "", fmt.Errorf("go tool -n %s returned no executable", name)
	}
	return path, nil
}

func checkVersion(path, name, expected string) {
	cmd := exec.Command(path, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		fatal("check "+name+" version", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output))))
	}
	actual := strings.TrimSpace(string(output))
	if !strings.Contains(actual, expected) {
		fatal("check "+name+" version", fmt.Errorf("got %q, want %s", actual, expected))
	}
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
