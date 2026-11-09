package main

import (
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func FuzzParseCLI(f *testing.F) {
	for _, args := range [][]string{
		{},
		{"-v", "gogo", "-i", "127.0.0.1"},
		{"--verbose", "scan", "-i", "127.0.0.1"},
		{"agent", "--", "scan"},
		{"-p", "gogo", "scan", "--model=", "--json"},
		{"--config=", "--data-dir", "scan", "gogo", "--help"},
		{"scan", "--", "--model", "gogo"},
		{"--max-tokens=0", "scan", "--verify=off", "-o", "jsonl"},
	} {
		f.Add(strings.Join(args, "\x00"))
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 8192 {
			t.Skip()
		}
		args := strings.Split(input, "\x00")
		original := slices.Clone(args)
		first, firstErr := parseCLI(args, io.Discard)
		if !slices.Equal(args, original) {
			t.Fatalf("CLI parsing changed its caller's arguments: before=%q after=%q", original, args)
		}
		second, secondErr := parseCLI(args, io.Discard)
		if fmt.Sprint(firstErr) != fmt.Sprint(secondErr) || first.Mode != second.Mode || first.Help != second.Help ||
			!slices.Equal(first.ScannerArgs, second.ScannerArgs) ||
			!reflect.DeepEqual(first.Option.MiscOptions, second.Option.MiscOptions) ||
			!reflect.DeepEqual(first.Option.LLMOptions, second.Option.LLMOptions) ||
			!reflect.DeepEqual(first.Option.AgentOptions, second.Option.AgentOptions) ||
			!reflect.DeepEqual(first.Option.Explicit, second.Option.Explicit) ||
			!reflect.DeepEqual(first.Option.Extensions, second.Option.Extensions) {
			t.Fatalf("CLI parsing is not repeatable for %q: first error=%v second error=%v", args, firstErr, secondErr)
		}
	})
}
