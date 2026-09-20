package config

import (
	"reflect"
	"strings"

	flags "github.com/jessevdk/go-flags"
)

// visitOptions follows the existing configuration schema, including newly added
// fields. Runtime-only members are deliberately outside that schema.
func visitOptions(option *Option, visit func(reflect.StructField, reflect.Value, string)) {
	var walk func(reflect.Value, string)
	walk = func(value reflect.Value, prefix string) {
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if !field.IsExported() || field.Tag.Get("config") == "-" {
				continue
			}
			key := field.Tag.Get("config")
			if key == "" {
				key = field.Name
			}
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			v := value.Field(i)
			if v.Kind() == reflect.Struct {
				walk(v, path)
				continue
			}
			visit(field, v, path)
		}
	}
	walk(reflect.ValueOf(option).Elem(), "")
}

// CaptureExplicitFlags distinguishes parser defaults from supplied flags. It
// also accepts flags consumed by the product's native-scanner argument adapter.
func CaptureExplicitFlags(option *Option, parser *flags.Parser) {
	if option.Explicit == nil {
		option.Explicit = make(map[string]bool)
	}
	for command := parser.Command; command != nil; command = command.Active {
		var walk func(*flags.Group)
		walk = func(group *flags.Group) {
			for _, flag := range group.Options() {
				if flag.IsSet() && !flag.IsSetDefault() {
					option.MarkExplicit(flag.LongName)
				}
			}
			for _, child := range group.Groups() {
				walk(child)
			}
		}
		walk(command.Group)
	}
}

func (o *Option) MarkExplicit(flag string) {
	if o.Explicit == nil {
		o.Explicit = make(map[string]bool)
	}
	o.Explicit[strings.TrimLeft(flag, "-")] = true
}

func (o *Option) fieldExplicit(field reflect.StructField, value reflect.Value) bool {
	if o.Explicit == nil {
		return !value.IsZero()
	}
	return o.Explicit[field.Tag.Get("long")] || o.Explicit[field.Tag.Get("short")] || o.Explicit[field.Name]
}

// explicitOptions removes defaults before evaluating environment precedence.
func explicitOptions(option *Option) Option {
	out := *option
	if option.Explicit == nil {
		return out
	}
	visitOptions(&out, func(field reflect.StructField, value reflect.Value, _ string) {
		if field.Name != "Extensions" && !option.fieldExplicit(field, value) {
			value.SetZero()
		}
	})
	if option.Resolved != nil {
		out.Extensions = CloneValues(option.Resolved.cli)
	}
	return out
}

func mergeOption(dst, src *Option) {
	source := make(map[string]reflect.Value)
	visitOptions(src, func(_ reflect.StructField, value reflect.Value, path string) { source[path] = value })
	visitOptions(dst, func(field reflect.StructField, value reflect.Value, path string) {
		if field.Name != "Extensions" && dst.fieldExplicit(field, value) {
			return
		}
		other := source[path]
		if src.present[path] || !other.IsZero() {
			value.Set(other)
		}
	})
	dst.present = src.present
}

func (o *Option) hasExplicit(name string) bool {
	field, ok := reflect.TypeOf(*o).FieldByName(name)
	return ok && o.fieldExplicit(field, reflect.ValueOf(o).Elem().FieldByName(name))
}
