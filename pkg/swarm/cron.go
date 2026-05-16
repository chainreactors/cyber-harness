package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/provider"
)

type cronState struct {
	task   CronTask
	cancel context.CancelFunc
}

type cronManager struct {
	mu    sync.Mutex
	items map[string]cronState
	ch    chan CronTask
	runFn func(context.Context, CronTask) error
}

func newCronManager(ch chan CronTask, runFn func(context.Context, CronTask) error) *cronManager {
	return &cronManager{
		items: make(map[string]cronState),
		ch:    ch,
		runFn: runFn,
	}
}

func (m *cronManager) Add(ctx context.Context, task CronTask) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.items[task.Name]; exists {
		return fmt.Errorf("cron %q already exists", task.Name)
	}
	cronCtx, cancel := context.WithCancel(ctx)
	m.items[task.Name] = cronState{task: task, cancel: cancel}
	go func() {
		ticker := time.NewTicker(task.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-cronCtx.Done():
				return
			case <-ticker.C:
				select {
				case m.ch <- task:
				case <-cronCtx.Done():
					return
				}
			}
		}
	}()
	return nil
}

func (m *cronManager) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	state, exists := m.items[name]
	if !exists {
		return fmt.Errorf("cron %q not found", name)
	}
	state.cancel()
	delete(m.items, name)
	return nil
}

func (m *cronManager) List() []CronTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]CronTask, 0, len(m.items))
	for _, state := range m.items {
		result = append(result, state.task)
	}
	return result
}

type CronTool struct {
	node *Node
}

func CronCommand(node *Node) *CronTool {
	return &CronTool{node: node}
}

func (t *CronTool) Name() string { return "cron" }

func (t *CronTool) Description() string {
	return "Manage scheduled recurring tasks on this swarm node. Use action 'create' to schedule a periodic task, 'list' to see active crons, 'delete' to remove one."
}

func (t *CronTool) Definition() provider.ToolDefinition {
	return provider.ToolDefinition{
		Type: "function",
		Function: provider.FunctionDefinition{
			Name:        "cron",
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"create", "list", "delete"},
						"description": "Action to perform",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Cron task name (required for create/delete)",
					},
					"interval": map[string]any{
						"type":        "string",
						"description": "How often to run, e.g. '5m', '1h', '30s' (required for create)",
					},
					"prompt": map[string]any{
						"type":        "string",
						"description": "What this cron task should do each time it fires (required for create)",
					},
				},
				"required": []string{"action"},
			},
		},
	}
}

func (t *CronTool) Execute(ctx context.Context, arguments string) (string, error) {
	var args struct {
		Action   string `json:"action"`
		Name     string `json:"name"`
		Interval string `json:"interval"`
		Prompt   string `json:"prompt"`
	}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch args.Action {
	case "create":
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required for create")
		}
		if strings.TrimSpace(args.Interval) == "" {
			return "", fmt.Errorf("interval is required for create")
		}
		if strings.TrimSpace(args.Prompt) == "" {
			return "", fmt.Errorf("prompt is required for create")
		}
		interval, err := time.ParseDuration(args.Interval)
		if err != nil {
			return "", fmt.Errorf("invalid interval %q: %w", args.Interval, err)
		}
		if interval < 10*time.Second {
			return "", fmt.Errorf("interval must be >= 10s")
		}
		task := CronTask{
			Name:         args.Name,
			Interval:     interval,
			Prompt:       args.Prompt,
			ContextLimit: t.node.cfg.HeartbeatContextLimit,
		}
		if err := t.node.AddCron(task); err != nil {
			return "", err
		}
		return fmt.Sprintf("Created cron %q: runs every %s", args.Name, interval), nil

	case "list":
		crons := t.node.ListCrons()
		if len(crons) == 0 {
			return "No active cron tasks.", nil
		}
		var sb strings.Builder
		for _, c := range crons {
			fmt.Fprintf(&sb, "- %s (every %s): %s\n", c.Name, c.Interval, c.Prompt)
		}
		return sb.String(), nil

	case "delete":
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name is required for delete")
		}
		if err := t.node.RemoveCron(args.Name); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted cron %q", args.Name), nil

	default:
		return "", fmt.Errorf("unknown action %q: use create, list, or delete", args.Action)
	}
}
