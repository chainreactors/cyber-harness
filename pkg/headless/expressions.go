//go:build browser

// Expression evaluation wrapping neutron's common.Evaluate.
// Replaces the hand-rolled {{var}} regex in action.go with full DSL support.

package headless

import (
	"fmt"
	"strings"

	"github.com/chainreactors/neutron/common"
)

// resolveActionArg resolves an action argument value by:
//  1. Getting the raw value from action args
//  2. Evaluating DSL expressions via neutron common.Evaluate
//     (supports {{rand_int()}}, {{replace()}}, {{base64()}} etc.)
//  3. Returns the resolved string
func resolveActionArg(act *Action, argName string, variables map[string]interface{}) (string, error) {
	raw := act.GetArg(argName)
	if raw == "" {
		return "", nil
	}
	return evaluateExpression(raw, variables)
}

// evaluateExpression resolves all {{...}} expressions in a string using neutron's DSL engine.
func evaluateExpression(data string, variables map[string]interface{}) (string, error) {
	if !strings.Contains(data, "{{") {
		return data, nil
	}
	result, err := common.Evaluate(data, variables)
	if err != nil {
		return data, nil
	}
	return result, nil
}

// resolveActionArgs resolves all arguments in an action's Data map.
func resolveActionArgs(act *Action, variables map[string]interface{}) map[string]string {
	if len(act.Data) == 0 {
		return act.Data
	}
	resolved := make(map[string]string, len(act.Data))
	for k, v := range act.Data {
		r, err := evaluateExpression(v, variables)
		if err != nil {
			resolved[k] = v
		} else {
			resolved[k] = r
		}
	}
	return resolved
}

// containsUnresolvedVariables checks for leftover {{...}} markers after evaluation.
func containsUnresolvedVariables(items ...string) error {
	for _, item := range items {
		if strings.Contains(item, "{{") && strings.Contains(item, "}}") {
			return fmt.Errorf("unresolved variable in: %s", item)
		}
	}
	return nil
}
