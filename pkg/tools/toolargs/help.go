package toolargs

import (
	"bytes"

	goflags "github.com/jessevdk/go-flags"
)

// NewGoFlagsParser returns the parser shared by a scanner's runtime argument
// handling and generated help. Keeping both paths on the same options struct
// prevents the CLI documentation from drifting away from accepted flags.
func NewGoFlagsParser(name string, data any) *goflags.Parser {
	parser := goflags.NewParser(data, goflags.Default&^goflags.PrintErrors)
	parser.Name = name
	parser.Usage = "[OPTIONS]"
	return parser
}

// GoFlagsHelp renders help directly from go-flags struct tags.
func GoFlagsHelp(name string, data any) string {
	parser := NewGoFlagsParser(name, data)
	if _, err := parser.ParseArgs([]string{"-h"}); err != nil {
		if flagsErr, ok := err.(*goflags.Error); ok && flagsErr.Type == goflags.ErrHelp {
			return flagsErr.Error()
		}
	}

	var buf bytes.Buffer
	parser.WriteHelp(&buf)
	return buf.String()
}
