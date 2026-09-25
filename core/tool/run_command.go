package tool

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

// RunCommand invokes one expanded argv. Registered commands take precedence
// over programs in PATH; a rejected registered command never falls through.
func RunCommand(ctx context.Context, registry CommandExecutor, argv []string, parent *Execution) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(argv) == 0 || argv[0] == "" {
		return nil, fmt.Errorf("empty command")
	}
	if parent == nil {
		return nil, fmt.Errorf("command %s requires an execution", argv[0])
	}
	name := argv[0]
	if registry != nil && !strings.ContainsAny(name, `/\`) && registry.Has(name) {
		return registry.Run(ctx, argv, parent)
	}
	env := mergeCommandEnvironment(os.Environ(), parent.Env)
	path, err := interp.LookPathDir(parent.Dir, expand.ListEnviron(env...), name)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, exec.ErrNotFound)
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Dir = parent.Dir
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = parent.Stdin, parent.Stdout, parent.Stderr
	if err := runExternalCommand(ctx, cmd); err != nil {
		return nil, err
	}
	return nil, nil
}

func mergeCommandEnvironment(base, overrides []string) []string {
	if len(overrides) == 0 {
		return base
	}
	values := make(map[string]string, len(overrides))
	for _, item := range overrides {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[environmentKey(key)] = value
		}
	}
	out := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if _, replaced := values[environmentKey(key)]; ok && replaced {
			continue
		}
		out = append(out, item)
	}
	return append(out, overrides...)
}

func environmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}
