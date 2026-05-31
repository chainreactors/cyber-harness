//go:build browser

package headless

import (
	"strings"

	"github.com/chainreactors/neutron/common"
)

// Action represents a single browser action in a headless template.
// Compatible with nuclei's headless action format.
type Action struct {
	Data        map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
	Name        string            `yaml:"name,omitempty" json:"name,omitempty"`
	Description string            `yaml:"description,omitempty" json:"description,omitempty"`
	ActionType  ActionTypeHolder  `yaml:"action" json:"action"`
}

func (a *Action) String() string {
	var b strings.Builder
	b.WriteString(a.ActionType.String())
	if a.Name != "" {
		b.WriteString(" name:")
		b.WriteString(a.Name)
	}
	for k, v := range a.Data {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte(':')
		b.WriteString(v)
	}
	return b.String()
}

// GetArg returns the raw value for a named argument.
func (a *Action) GetArg(name string) string {
	if a.Data == nil {
		return ""
	}
	return a.Data[name]
}

// ActionData is the accumulated output map flowing through action execution.
type ActionData map[string]interface{}

// Interpolate resolves {{var}} and DSL expressions in all action arguments
// using neutron's common.Evaluate. Returns a new Action; original is not modified.
func (a *Action) Interpolate(data map[string]interface{}) *Action {
	if len(a.Data) == 0 {
		return a
	}
	resolved := &Action{
		Name:        a.Name,
		Description: a.Description,
		ActionType:  a.ActionType,
		Data:        make(map[string]string, len(a.Data)),
	}
	for k, v := range a.Data {
		if !strings.Contains(v, "{{") {
			resolved.Data[k] = v
			continue
		}
		result, err := common.Evaluate(v, data)
		if err != nil {
			resolved.Data[k] = v
		} else {
			resolved.Data[k] = result
		}
	}
	return resolved
}
