package commands

import (
	"context"
	"errors"

	"github.com/chainreactors/aiscan/core/commandline"
	coreregistry "github.com/chainreactors/aiscan/core/registry"
)

var (
	ErrInvalidCommand   = errors.New("invalid command registration")
	ErrDuplicateCommand = coreregistry.ErrDuplicate
	ErrUnavailable      = coreregistry.ErrUnavailable
)

// Command is an immutable native command declaration. Its dependencies are
// captured by Run when the owning extension is constructed.
type Command struct {
	Name            string
	Usage           string
	QuickReference  string
	DescriptionPath string
	Run             func(context.Context, *Execution) (any, error)
}

func normalizeNoColor(name string, args []string) []string {
	if name != "scan" {
		return args
	}
	for _, arg := range args {
		if arg == "--no-color" {
			return args
		}
	}
	return append(args, "--no-color")
}

func SplitCommandLine(input string) ([]string, error) { return commandline.SplitCommandLine(input) }
func JoinCommandLine(name string, args []string) string {
	return commandline.JoinCommandLine(name, args)
}
