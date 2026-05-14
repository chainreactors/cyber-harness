package tools

import (
	"context"
	"fmt"
	"io"
	"runtime/debug"
	"strings"

	"github.com/chainreactors/aiscan/pkg/tools/scan"
)

type PseudoCommand interface {
	Name() string
	Usage() string
	Execute(ctx context.Context, args []string) (string, error)
}

type StreamingCommand interface {
	PseudoCommand
	ExecuteStreaming(ctx context.Context, args []string, stream io.Writer) (string, error)
}

type ScannerRegistry struct {
	items map[string]PseudoCommand
	order []string
}

func NewScannerRegistry() *ScannerRegistry {
	return &ScannerRegistry{items: make(map[string]PseudoCommand)}
}

func (r *ScannerRegistry) Register(cmd PseudoCommand) {
	name := cmd.Name()
	if _, exists := r.items[name]; !exists {
		r.order = append(r.order, name)
	}
	r.items[name] = cmd
}

func (r *ScannerRegistry) Get(name string) (PseudoCommand, bool) {
	cmd, ok := r.items[name]
	return cmd, ok
}

func (r *ScannerRegistry) Has(name string) bool {
	_, ok := r.items[name]
	return ok
}

func (r *ScannerRegistry) All() []PseudoCommand {
	result := make([]PseudoCommand, 0, len(r.order))
	for _, name := range r.order {
		result = append(result, r.items[name])
	}
	return result
}

func (r *ScannerRegistry) Names() []string {
	return append([]string(nil), r.order...)
}

func (r *ScannerRegistry) ConfigureScan(opts ...scan.Option) {
	cmd, ok := r.Get("scan")
	if !ok {
		return
	}
	scanCmd, ok := cmd.(*scan.Command)
	if !ok || scanCmd == nil {
		return
	}
	scanCmd.Configure(opts...)
}

func (r *ScannerRegistry) Execute(ctx context.Context, cmdLine string) (string, error) {
	tokens, err := splitCommandLine(cmdLine)
	if err != nil {
		return "", err
	}
	return r.ExecuteArgs(ctx, tokens)
}

func (r *ScannerRegistry) ExecuteArgs(ctx context.Context, tokens []string) (out string, err error) {
	return r.ExecuteArgsStreaming(ctx, tokens, nil)
}

func (r *ScannerRegistry) ExecuteArgsStreaming(ctx context.Context, tokens []string, stream io.Writer) (out string, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			out = ""
			err = fmt.Errorf("scanner command panic: %v\n%s", recovered, debug.Stack())
		}
	}()

	if len(tokens) == 0 {
		return "", fmt.Errorf("empty command")
	}

	name := tokens[0]
	cmd, ok := r.Get(name)
	if !ok {
		return "", fmt.Errorf("unknown scanner command: %s", name)
	}

	args := tokens[1:]
	if stream != nil {
		if streaming, ok := cmd.(StreamingCommand); ok {
			out, err = streaming.ExecuteStreaming(ctx, args, stream)
			return out, err
		}
	}
	out, err = cmd.Execute(ctx, args)
	return out, err
}

func (r *ScannerRegistry) UsageDocs() string {
	var sb strings.Builder
	for _, cmd := range r.All() {
		sb.WriteString("```\n")
		sb.WriteString(cmd.Usage())
		sb.WriteString("\n```\n\n")
	}
	return sb.String()
}
