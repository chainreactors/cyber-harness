package relay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	coretool "github.com/chainreactors/cyber/core/tool"
)

// operation is one data-plane call; the table is generated from the spec.
type operation struct {
	Name, Method, Path, Tag, Summary, Description string
	Fields                                        []field
	Body                                          func() any // nil without a request body
	Params                                        func() any // nil without query parameters
	Call                                          func(ctx context.Context, c *Client, path []string, body, params any) (any, error)
}

// field documents one input: a path argument, a body field or a query parameter.
type field struct {
	Name, In, Type, Doc string
	Required            bool
}

// DescriptionPath is the cyber-relay skill that documents the command.
const DescriptionPath = "cyber://skills/relay/SKILL.md"

// NewCommand exposes every operation of the generated table as
// `relay <operation>`. Flags are the spec's field names; `relay <operation> -h`
// prints them with the spec's descriptions.
func NewCommand(client *Client) coretool.Command {
	return coretool.Command{
		Name:            "relay",
		Usage:           overview(),
		DescriptionPath: DescriptionPath,
		Run: func(ctx context.Context, execution *coretool.Execution) (any, error) {
			return nil, run(ctx, client, execution.Dir, execution.Args, execution.Stdout)
		},
	}
}

func lookup(name string) (*operation, bool) {
	for i := range operations {
		if operations[i].Name == name {
			return &operations[i], true
		}
	}
	return nil, false
}

func overview() string {
	var b strings.Builder
	b.WriteString("relay <operation> [arguments] [--field value ...] - call the configured Cyber Relay gateway\n\n")
	b.WriteString("Run `relay <operation> -h` for its fields. Operations:\n")
	byTag := map[string][]operation{}
	var tags []string
	for _, op := range operations {
		if _, seen := byTag[op.Tag]; !seen {
			tags = append(tags, op.Tag)
		}
		byTag[op.Tag] = append(byTag[op.Tag], op)
	}
	sort.Strings(tags)
	for _, tag := range tags {
		fmt.Fprintf(&b, "\n  %s\n", tag)
		for _, op := range byTag[tag] {
			fmt.Fprintf(&b, "    %-24s %s\n", op.Name, op.Summary)
		}
	}
	return b.String()
}

func help(op *operation) string {
	var b strings.Builder
	fmt.Fprintf(&b, "relay %s - %s\n%s %s\n", op.Name, op.Summary, op.Method, op.Path)
	if op.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", op.Description)
	}
	fmt.Fprintf(&b, "\nUsage: relay %s%s [--field value ...]\n", op.Name, synopsis(op))
	var flags []field
	for i, f := range op.Fields {
		if f.In == "path" {
			fmt.Fprintf(&b, "  <%s>  %s\n", f.Name, f.Doc)
			continue
		}
		// A query parameter that repeats a body field is the same flag: the
		// body field takes it (fieldNamed), so list it once.
		if first, _ := fieldNamed(op, f.Name); first != &op.Fields[i] {
			continue
		}
		flags = append(flags, f)
	}
	if len(flags) > 0 {
		b.WriteString("\nFields:\n")
		for _, f := range flags {
			required := ""
			if f.Required {
				required = ", required"
			}
			fmt.Fprintf(&b, "  --%s (%s%s)  %s\n", flagName(f.Name), label(f), required, f.Doc)
		}
	}
	if op.Body != nil {
		b.WriteString("  --body <json|@file>  the whole request body; flags override its fields\n")
		if primary := primaryField(op); primary != nil {
			fmt.Fprintf(&b, "\nWords after the arguments fill --%s.\n", flagName(primary.Name))
		}
		if hasImage(op) {
			b.WriteString("An image field given as @path sends that file base64-encoded.\n")
		}
	}
	return b.String()
}

func synopsis(op *operation) string {
	var b strings.Builder
	for _, f := range op.Fields {
		if f.In == "path" {
			b.WriteString(" <" + f.Name + ">")
		}
	}
	if primary := primaryField(op); primary != nil {
		b.WriteString(" [" + primary.Name + "]")
	}
	return b.String()
}

// primaryField is the required body field positional words fill.
func primaryField(op *operation) *field {
	for i, f := range op.Fields {
		if f.In == "body" && f.Required && (f.Type == "string" || f.Type == "[]string") {
			return &op.Fields[i]
		}
	}
	return nil
}

func hasImage(op *operation) bool {
	for _, f := range op.Fields {
		if isImage(f) {
			return true
		}
	}
	return false
}

func isImage(f field) bool {
	return f.In == "body" && f.Type == "string" && strings.Contains(f.Name, "image")
}

func flagName(name string) string { return strings.ReplaceAll(name, "_", "-") }

// Body fields carry Go types and query parameters spec types; help shows both
// the way a flag takes them.
func isBool(f field) bool { return f.Type == "bool" || f.Type == "*bool" || f.Type == "boolean" }

func label(f field) string {
	switch {
	case isBool(f):
		return "flag, or =false"
	case f.Type == "int" || f.Type == "int64" || f.Type == "integer":
		return "integer"
	case f.Type == "float64" || f.Type == "number":
		return "number"
	case strings.HasPrefix(f.Type, "[]"):
		return "comma-separated list"
	case strings.HasPrefix(f.Type, "map["):
		return "key=value, repeatable"
	}
	return "string"
}

func run(ctx context.Context, client *Client, dir string, args []string, stdout io.Writer) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		_, err := io.WriteString(stdout, overview())
		return err
	}
	op, ok := lookup(args[0])
	if !ok {
		return fmt.Errorf("relay: unknown operation %q; run `relay -h`", args[0])
	}
	path, body, params, err := parse(op, dir, args[1:])
	if errors.Is(err, errHelp) {
		_, err := io.WriteString(stdout, help(op))
		return err
	}
	if err != nil {
		return fmt.Errorf("relay %s: %w\n\n%s", op.Name, err, help(op))
	}
	out, err := op.Call(ctx, client, path, body, params)
	if err != nil {
		return err
	}
	// Native queries and failover chains carry "&&" and "->": keep them readable.
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

var errHelp = errors.New("help")

func parse(op *operation, dir string, args []string) (path []string, body, params any, err error) {
	if op.Body != nil {
		body = op.Body()
	}
	if op.Params != nil {
		params = op.Params()
	}
	var words []string
	type assignment struct{ field, value string }
	var assignments []assignment
	var rawBody string
	var hasBody bool
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			words = append(words, args[i+1:]...)
			break
		}
		if args[i] == "-h" || args[i] == "--help" {
			return nil, nil, nil, errHelp
		}
		name, ok := strings.CutPrefix(args[i], "--")
		if !ok || name == "" {
			words = append(words, args[i])
			continue
		}
		name, value, inline := strings.Cut(name, "=")
		name = strings.ReplaceAll(name, "-", "_")
		if name == "body" && body != nil {
			if !inline {
				if i+1 >= len(args) {
					return nil, nil, nil, fmt.Errorf("--body requires a value")
				}
				i++
				value = args[i]
			}
			rawBody = value
			hasBody = true
			continue
		}
		f, ok := fieldNamed(op, name)
		if !ok {
			return nil, nil, nil, fmt.Errorf("unknown field --%s", flagName(name))
		}
		if !inline {
			if isBool(*f) {
				value = "true"
			} else {
				if i+1 >= len(args) {
					return nil, nil, nil, fmt.Errorf("--%s requires a value", flagName(name))
				}
				i++
				value = args[i]
			}
		}
		assignments = append(assignments, assignment{name, value})
	}
	if hasBody {
		data := []byte(rawBody)
		if file, ok := strings.CutPrefix(rawBody, "@"); ok {
			if data, err = os.ReadFile(resolve(dir, file)); err != nil {
				return nil, nil, nil, fmt.Errorf("--body: %w", err)
			}
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(body); err != nil {
			return nil, nil, nil, fmt.Errorf("--body: %w", err)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return nil, nil, nil, fmt.Errorf("--body: unexpected trailing data")
		}
	}
	for _, f := range op.Fields {
		if f.In != "path" {
			continue
		}
		if len(words) == 0 {
			return nil, nil, nil, fmt.Errorf("missing <%s>", f.Name)
		}
		path, words = append(path, words[0]), words[1:]
	}
	if len(words) > 0 {
		primary := primaryField(op)
		if primary == nil {
			return nil, nil, nil, fmt.Errorf("unexpected argument %q", words[0])
		}
		if primary.Type == "string" {
			words = []string{strings.Join(words, " ")}
		}
		for _, word := range words {
			assignments = append(assignments, assignment{primary.Name, word})
		}
	}
	cleared := map[string]bool{}
	for _, a := range assignments {
		f, _ := fieldNamed(op, a.field)
		target := body
		if f.In == "query" {
			target = params
		}
		key := f.In + ":" + f.Name
		if !cleared[key] {
			if v := structField(target, f.Name); v.Kind() == reflect.Slice {
				v.SetZero()
			}
			cleared[key] = true
		}
		if err := set(target, *f, a.value, dir); err != nil {
			return nil, nil, nil, err
		}
	}
	for _, f := range op.Fields {
		if f.In == "body" && f.Required && structField(body, f.Name).IsZero() {
			return nil, nil, nil, fmt.Errorf("missing required --%s", flagName(f.Name))
		}
	}
	return path, body, params, nil
}

func fieldNamed(op *operation, name string) (*field, bool) {
	for i, f := range op.Fields {
		if f.Name == name && f.In != "path" {
			return &op.Fields[i], true
		}
	}
	return nil, false
}

func resolve(dir, file string) string {
	if filepath.IsAbs(file) || dir == "" {
		return file
	}
	return filepath.Join(dir, file)
}

// structField finds the field of *target whose JSON name is name.
func structField(target any, name string) reflect.Value {
	v := reflect.ValueOf(target).Elem()
	for i := 0; i < v.NumField(); i++ {
		tag, _, _ := strings.Cut(v.Type().Field(i).Tag.Get("json"), ",")
		if tag == name {
			return v.Field(i)
		}
	}
	panic("relay: generated field " + name + " is missing")
}

func set(target any, f field, raw, dir string) error {
	v := structField(target, f.Name)
	invalid := func(err error) error { return fmt.Errorf("--%s: %w", flagName(f.Name), err) }
	if file, ok := strings.CutPrefix(raw, "@"); ok && isImage(f) {
		data, err := os.ReadFile(resolve(dir, file))
		if err != nil {
			return invalid(err)
		}
		raw = base64.StdEncoding.EncodeToString(data)
	}
	switch {
	case v.Kind() == reflect.Pointer && v.Type().Elem().Kind() == reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return invalid(err)
		}
		v.Set(reflect.ValueOf(&b))
	case v.Kind() == reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return invalid(err)
		}
		v.SetBool(b)
	case v.Kind() == reflect.String:
		v.SetString(raw)
	case v.Kind() == reflect.Int || v.Kind() == reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return invalid(err)
		}
		v.SetInt(n)
	case v.Kind() == reflect.Float64:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return invalid(err)
		}
		v.SetFloat(n)
	case v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.String:
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				v.Set(reflect.Append(v, reflect.ValueOf(item).Convert(v.Type().Elem())))
			}
		}
	case v.Kind() == reflect.Map && v.Type().Key().Kind() == reflect.String:
		key, value, ok := strings.Cut(raw, "=")
		if !ok || key == "" {
			return invalid(fmt.Errorf("expected key=value"))
		}
		if v.IsNil() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		v.SetMapIndex(reflect.ValueOf(key), reflect.ValueOf(value).Convert(v.Type().Elem()))
	default:
		return invalid(fmt.Errorf("type %s cannot be set from a flag; use --body", v.Type()))
	}
	return nil
}
