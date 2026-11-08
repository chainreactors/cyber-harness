package config

import (
	"path/filepath"
	"testing"

	flags "github.com/jessevdk/go-flags"
)

func TestInboxCommandDefaultsAndExplicitTimeout(t *testing.T) {
	for _, test := range []struct {
		command RunMode
		extra   string
		timeout int
	}{{RunModeAgent, "", 3600}, {RunModeInbox, "", 0}, {RunModeInbox, "  timeout: 17\n", 17}} {
		t.Run(string(test.command)+test.extra, func(t *testing.T) {
			c := isolatedContext(t)
			document := "inbox:\n  state_dir: durable\n"
			if test.extra != "" {
				document += "agent:\n" + test.extra
			}
			putConfig(t, filepath.Join(c.Directory, DefaultConfigName), document)
			o := &Option{Command: test.command, Context: c, Explicit: map[string]bool{}, AgentOptions: AgentOptions{Timeout: 3600}}
			if _, err := ResolveToolRuntimeConfig(o); err != nil {
				t.Fatal(err)
			}
			if o.Timeout != test.timeout || o.MaxActive != 4 || o.StateDir != "durable" || o.Command != test.command {
				t.Fatalf("command=%s timeout=%d inbox=%+v", o.Command, o.Timeout, o.InboxOptions)
			}
		})
	}
}

func TestInboxExplicitCLITimeout(t *testing.T) {
	for _, value := range []string{"0", "19"} {
		t.Run(value, func(t *testing.T) {
			o := &Option{Command: RunModeInbox, Context: isolatedContext(t)}
			parser := flags.NewParser(o, flags.Default&^flags.PrintErrors)
			if _, err := parser.ParseArgs([]string{"--timeout=" + value}); err != nil {
				t.Fatal(err)
			}
			CaptureExplicitFlags(o, parser)
			if _, err := ResolveToolRuntimeConfig(o); err != nil {
				t.Fatal(err)
			}
			want := 0
			if value == "19" {
				want = 19
			}
			if o.Timeout != want {
				t.Fatalf("explicit timeout replaced: %d", o.Timeout)
			}
		})
	}
	for _, option := range []*Option{
		{InboxOptions: InboxOptions{MaxActive: -1}},
		{AgentOptions: AgentOptions{Resume: "history.jsonl"}},
	} {
		option.Command = RunModeInbox
		if err := validateExecutionOptions(option); err == nil {
			t.Fatalf("accepted invalid inbox options: %+v", option)
		}
	}
}

func TestConfigCannotSelectInboxCommand(t *testing.T) {
	for _, document := range []string{"agent:\n  mode: active\n", "command: inbox\n"} {
		c := isolatedContext(t)
		putConfig(t, filepath.Join(c.Directory, DefaultConfigName), document)
		option := &Option{Context: c, Command: RunModeAgent}
		if _, err := ResolveToolRuntimeConfig(option); err == nil {
			t.Fatalf("configuration selected a command: %s", document)
		}
	}
}
