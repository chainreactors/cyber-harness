//go:build full

package headless

import (
	"fmt"
	"os"

	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/chainreactors/neutron/protocols/executer"
	"gopkg.in/yaml.v3"
)

// Template represents a headless browser automation template.
// Compatible with nuclei's headless template format.
type Template struct {
	// ID is the unique template identifier.
	ID string `yaml:"id" json:"id"`

	// Info contains metadata about the template.
	Info TemplateInfo `yaml:"info" json:"info"`

	// Variables contains template-level variables with DSL expressions.
	// Evaluated at compile time (e.g., rand_int, replace, base64).
	Variables protocols.Variable `yaml:"variables,omitempty" json:"variables,omitempty"`

	// Headless contains the headless request definitions.
	RequestsHeadless []*Request `yaml:"headless" json:"headless"`

	// TotalRequests is computed at compile time.
	TotalRequests int `yaml:"-" json:"-"`

	// Executor runs the compiled requests.
	Executor *executer.Executer `yaml:"-" json:"-"`
}

// TemplateInfo contains metadata about the template.
type TemplateInfo struct {
	Name        string `yaml:"name" json:"name"`
	Author      string `yaml:"author,omitempty" json:"author,omitempty"`
	Severity    string `yaml:"severity,omitempty" json:"severity,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Tags        string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

// LoadTemplate parses a headless template from a YAML file.
func LoadTemplate(path string) (*Template, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read template %s: %w", path, err)
	}
	return ParseTemplate(data)
}

// ParseTemplate parses a headless template from YAML bytes.
func ParseTemplate(data []byte) (*Template, error) {
	var t Template
	if err := yaml.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parse template: %w", err)
	}
	if len(t.RequestsHeadless) == 0 {
		return nil, fmt.Errorf("no headless requests defined in template")
	}
	// Post-process: fix null action types (YAML `action:` with no value).
	// In nuclei, a bare action with args.code is treated as script.
	for _, req := range t.RequestsHeadless {
		for _, step := range req.Steps {
			if step.ActionType.ActionType == 0 {
				if step.GetArg("code") != "" {
					step.ActionType.ActionType = ActionScript
				}
			}
		}
	}
	return &t, nil
}

// Compile prepares the template for execution with the given engine.
func (t *Template) Compile(engine *Engine, options *protocols.ExecuterOptions) error {
	if options == nil {
		options = &protocols.ExecuterOptions{Options: &protocols.Options{}}
	}

	// Inject template-level variables into executer options.
	if t.Variables.Len() > 0 {
		options.Variables = t.Variables
	}

	var requests []protocols.Request
	for _, req := range t.RequestsHeadless {
		req.SetEngine(engine)
		requests = append(requests, req)
	}

	t.Executor = executer.NewExecuter(requests, options)
	if err := t.Executor.Compile(); err != nil {
		return fmt.Errorf("compile template %s: %w", t.ID, err)
	}
	t.TotalRequests = t.Executor.Requests()
	return nil
}

// Execute runs the template against a target URL.
func (t *Template) Execute(target string, payload map[string]interface{}) (*operators.Result, error) {
	if t.Executor == nil {
		return nil, fmt.Errorf("template not compiled")
	}
	return t.Executor.Execute(protocols.NewScanContext(target, payload))
}

// ExecuteWithCallback runs the template and invokes callback for each result event.
func (t *Template) ExecuteWithCallback(target string, payload map[string]interface{}, callback func(*protocols.ResultEvent)) (*operators.Result, error) {
	if t.Executor == nil {
		return nil, fmt.Errorf("template not compiled")
	}
	ctx := protocols.NewScanContext(target, payload)
	ctx.OnResult = func(e *protocols.InternalWrappedEvent) {
		if e == nil {
			return
		}
		for _, req := range t.RequestsHeadless {
			for _, result := range req.MakeResultEvent(e) {
				callback(result)
			}
		}
	}
	return t.Executor.Execute(ctx)
}
