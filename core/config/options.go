package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/skills"
)

const Version = "0.1.0"

type Option struct {
	LLMOptions     `group:"LLM Options" config:"llm"`
	ScannerOptions `group:"Scanner Options" config:"cyberhub"`
	AgentOptions   `group:"Agent Options" config:"agent"`
	IOAOptions     `group:"IOA Options" config:"ioa"`
	ReconOptions   `group:"Recon Options" config:"recon"`
	MiscOptions    `group:"Miscellaneous Options" config:"misc"`
	ScanConfig     ScanConfigOptions `no-flag:"true" config:"scan"`
}

type ScanConfigOptions struct {
	Verify        string `config:"verify"`
	VerifyTimeout int    `config:"verify_timeout"`
}

type LLMOptions struct {
	Provider  string             `long:"provider" config:"provider" description:"LLM provider type: openai (default, OpenAI-compatible) or anthropic"`
	BaseURL   string             `long:"base-url" config:"base_url" description:"LLM API base URL"`
	APIKey    string             `long:"api-key" config:"api_key" description:"LLM API key (or set env: OPENAI_API_KEY, AISCAN_API_KEY)"`
	Model     string             `long:"model" config:"model" description:"LLM model name"`
	LLMProxy  string             `long:"llm-proxy" config:"proxy" description:"Proxy for LLM API requests"`
	Providers []LLMProviderEntry `no-flag:"true" config:"providers" yaml:"providers"`
	AI        bool               `long:"ai" description:"Analyze direct scanner output with an LLM"`
}

type LLMProviderEntry struct {
	Provider string `config:"provider" yaml:"provider"`
	BaseURL  string `config:"base_url" yaml:"base_url"`
	APIKey   string `config:"api_key" yaml:"api_key"`
	Model    string `config:"model" yaml:"model"`
	Proxy    string `config:"proxy" yaml:"proxy"`
	Timeout  int    `config:"timeout" yaml:"timeout"`
}

type ScannerOptions struct {
	CyberhubURL  string `long:"cyberhub-url" config:"url" description:"Cyberhub server URL for loading fingers/templates"`
	CyberhubKey  string `long:"cyberhub-key" config:"key" description:"Cyberhub API key"`
	CyberhubMode string `long:"cyberhub-mode" config:"mode" description:"Cyberhub resource mode: merge or override"`
	Proxy        string `long:"proxy" config:"proxy" description:"Proxy for scanner tools. Supports socks5://, trojan://, vless://, clash:// (subscription with load balancing)"`
}

type AgentOptions struct {
	Prompt         string   `short:"p" long:"prompt" description:"Natural language task for the agent"`
	Inputs         []string `short:"i" long:"input" description:"Target input: IP, URL, IP:port, or CIDR. Can specify multiple"`
	Skills         []string `short:"s" long:"skill" description:"Skill to apply (name or file path). Can specify multiple"`
	TaskFile       string   `long:"task-file" description:"File containing task description"`
	Heartbeat      int      `long:"heartbeat" description:"Heartbeat interval in minutes: periodically wake the agent to review context (0 disables)" default:"0"`
	Timeout        int      `long:"timeout" config:"timeout" description:"Overall timeout in seconds" default:"3600"`
	EvalCriteria   string   `short:"e" long:"eval" config:"eval_criteria" description:"Goal evaluation criteria — an independent LLM evaluates whether the task was achieved"`
	EvalModel      string   `long:"eval-model" config:"eval_model" description:"Model for goal evaluation (defaults to main model)"`
	EvalMaxRetries int      `long:"eval-retries" config:"eval_retries" description:"Max goal evaluation retry rounds" default:"3"`
	WebURL         string   `long:"web-url" config:"web_url" description:"AIScan web server URL for remote REPL and PTY access"`
}

type IOAOptions struct {
	IOAURL      string `long:"ioa-url" config:"url" description:"IOA server URL (supports http://token@host:port for auth)"`
	IOAToken    string `long:"ioa-token" config:"token" description:"IOA server access key (for 'ioa serve'; auto-generated if empty)"`
	IOANodeID   string `long:"ioa-node-id" description:"Existing IOA node id for agent tools"`
	IOANodeName string `long:"ioa-node-name" config:"node_name" description:"IOA node name when auto-registering"`
	Space       string `long:"space" config:"space" description:"IOA space name" default:"default"`
	IOAJSON     bool   `long:"json" description:"Output IOA query results in JSON format"`
}

type MiscOptions struct {
	ConfigFile string `short:"c" long:"config" description:"Path to config file (default: ./config.yaml, ~/.config/aiscan/config.yaml)"`
	InitConfig bool   `long:"init" description:"Generate default config.yaml and exit"`
	ViewFile   string `short:"F" long:"view" description:"View a scan record JSONL file"`
	ViewFormat string `short:"o" long:"output" description:"Output format for -F: terminal (default), markdown" default:"terminal"`
	ViewOutput string `short:"f" long:"file" description:"Write -F output to file instead of stdout"`
	Debug      bool   `long:"debug" config:"debug" description:"Enable debug logging"`
	Verbose    []bool `short:"v" long:"verbose" description:"Increase verbosity (-v tools, -vv thinking)"`
	Quiet      bool   `short:"q" long:"quiet" config:"quiet" description:"Quiet mode — only show final result"`
	NoColor    bool   `long:"no-color" config:"no_color" description:"Disable ANSI colors in scanner output"`
	Version    bool   `long:"version" description:"Print version and exit"`
}

type RunMode string

const (
	RunModeAgent       RunMode = "agent"
	RunModeIOAServe    RunMode = "ioa serve"
	RunModeIOASpaces   RunMode = "ioa spaces"
	RunModeIOAMessages RunMode = "ioa messages"
	RunModeIOAContext  RunMode = "ioa context"
	RunModeIOANodes    RunMode = "ioa nodes"
	RunModeScanner     RunMode = "scanner"
	RunModeNoCommand   RunMode = ""
)

type IOAClientArgs struct {
	Space     string
	MessageID string
}

func HasAgentOneShotInput(opt *Option) bool {
	if strings.TrimSpace(opt.Prompt) != "" || opt.TaskFile != "" || len(opt.Inputs) > 0 {
		return true
	}
	return !StdinIsTerminal()
}

func StdinIsTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

func ResolveTask(opt *Option) (string, error) {
	prompt := strings.TrimSpace(opt.Prompt)
	if prompt != "" {
		if len(opt.Inputs) > 0 {
			return fmt.Sprintf("%s\n\nTargets:\n%s", prompt, FormatInputs(opt.Inputs)), nil
		}
		return prompt, nil
	}

	if opt.TaskFile != "" {
		data, err := os.ReadFile(opt.TaskFile)
		if err != nil {
			return "", fmt.Errorf("read task file: %w", err)
		}
		task := strings.TrimSpace(string(data))
		if len(opt.Inputs) > 0 {
			return fmt.Sprintf("%s\n\nTargets:\n%s", task, FormatInputs(opt.Inputs)), nil
		}
		return task, nil
	}

	if !StdinIsTerminal() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		task := strings.TrimSpace(string(data))
		if task != "" {
			if len(opt.Inputs) > 0 {
				return fmt.Sprintf("%s\n\nTargets:\n%s", task, FormatInputs(opt.Inputs)), nil
			}
			return task, nil
		}
	}

	if len(opt.Inputs) > 0 {
		return fmt.Sprintf("Scan the provided targets using scan and summarize results.\n\nTargets:\n%s", FormatInputs(opt.Inputs)), nil
	}

	return "", fmt.Errorf("no prompt specified: use -p, --prompt, --task-file, or pipe via stdin")
}

func FormatInputs(inputs []string) string {
	var sb strings.Builder
	for _, input := range inputs {
		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(input)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func ApplySelectedSkills(text string, selected []string, store *skills.Store) (string, error) {
	if len(selected) == 0 {
		return text, nil
	}
	var sb strings.Builder
	for _, name := range selected {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if skill, ok := store.ByName(name); ok {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(store.FormatInvocation(skill, ""))
			continue
		}
		body := skills.ReadFile("skills/" + name + ".md")
		if body == "" {
			body = skills.ReadFile(name)
		}
		if body == "" {
			return "", fmt.Errorf("unknown skill %q", name)
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(body)
	}
	if strings.TrimSpace(text) != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(strings.TrimSpace(text))
	}
	return sb.String(), nil
}
