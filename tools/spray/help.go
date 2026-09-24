package spray

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/chainreactors/cyber/tools/toolargs"
	spraycore "github.com/chainreactors/spray/core"
	flags "github.com/jessevdk/go-flags"
)

const (
	helpWidth       = 96
	helpDescription = 32
)

func sprayHelp() string {
	var options spraycore.Option
	parser := toolargs.NewGoFlagsParser("spray", &options)
	var out strings.Builder
	out.WriteString("Usage:\n  spray [OPTIONS]\n")

	var writeGroup func(*flags.Group)
	writeGroup = func(group *flags.Group) {
		if group.Hidden {
			return
		}
		var visible []*flags.Option
		for _, option := range group.Options() {
			if !option.Hidden && option.Description != "" {
				visible = append(visible, option)
			}
		}
		if len(visible) > 0 {
			out.WriteByte('\n')
			if group.ShortDescription != "" {
				fmt.Fprintf(&out, "%s:\n", group.ShortDescription)
			}
			for _, option := range visible {
				writeHelpOption(&out, option)
			}
		}
		for _, child := range group.Groups() {
			writeGroup(child)
		}
	}
	writeGroup(parser.Group)
	out.WriteString("\nHelp Options:\n  -h, --help                    Show this help message\n")
	return out.String()
}

func writeHelpOption(out *strings.Builder, option *flags.Option) {
	name := "  "
	if option.ShortName != 0 {
		name += fmt.Sprintf("-%c", option.ShortName)
	}
	if option.LongName != "" {
		if option.ShortName != 0 {
			name += ", "
		}
		name += "--" + option.LongNameWithNamespace()
	}
	valueType := option.Field().Type
	if valueType.Kind() != reflect.Bool && !(valueType.Kind() == reflect.Slice && valueType.Elem().Kind() == reflect.Bool) {
		valueName := option.ValueName
		if valueName == "" {
			valueName = "value"
		}
		name += " <" + valueName + ">"
	}

	description := option.Description
	if len(option.Default) > 0 {
		description += " (default: " + strings.Join(option.Default, ", ") + ")"
	}
	if len(option.Choices) > 0 {
		description += "; choices: " + strings.Join(option.Choices, ", ")
	}
	if len(name) >= helpDescription {
		out.WriteString(name)
		out.WriteByte('\n')
		name = ""
	}
	out.WriteString(name)
	out.WriteString(strings.Repeat(" ", helpDescription-len(name)))
	writeWrappedHelp(out, description)
}

func writeWrappedHelp(out *strings.Builder, description string) {
	column := helpDescription
	for _, word := range strings.Fields(description) {
		if column > helpDescription && column+1+len(word) > helpWidth {
			out.WriteByte('\n')
			out.WriteString(strings.Repeat(" ", helpDescription))
			column = helpDescription
		}
		if column > helpDescription {
			out.WriteByte(' ')
			column++
		}
		out.WriteString(word)
		column += len(word)
	}
	out.WriteByte('\n')
}
