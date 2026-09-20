package agent

import (
	types "github.com/chainreactors/cyber/core/types"
	"strings"
)

func delegationFromToolCall(toolName string, args any) (*types.DelegationDetail, bool) {
	if toolName != "subagent" {
		return nil, false
	}
	values, ok := args.(map[string]any)
	if !ok {
		return nil, false
	}
	if action, _ := values["action"].(string); action != "" && action != "create" {
		return nil, false
	}
	task, _ := values["prompt"].(string)
	if strings.TrimSpace(task) == "" {
		return nil, false
	}
	name, _ := values["name"].(string)
	typeName, _ := values["type"].(string)
	mode, _ := values["mode"].(string)
	return delegationDetail(task, typeName, name, mode), true
}

func delegationDetail(task, typeName, name, mode string) *types.DelegationDetail {
	detail := &types.DelegationDetail{
		Task:      task,
		AgentName: name,
		AgentType: typeName,
	}
	switch mode {
	case "sync":
		detail.RunMode = types.DelegationRunForeground
		detail.ContextMode = types.DelegationContextFresh
	case "async":
		detail.RunMode = types.DelegationRunBackground
		detail.ContextMode = types.DelegationContextFresh
	case "fork":
		detail.RunMode = types.DelegationRunBackground
		detail.ContextMode = types.DelegationContextFork
	}
	return detail
}
