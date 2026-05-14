package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/ioa"
	acpclient "github.com/chainreactors/ioa/client"
)

type Config struct {
	Client       acpclient.StreamAPI
	Provider     provider.Provider
	Tools        *command.CommandRegistry
	SystemPrompt string
	Model        string
	Stream       bool

	NodeName              string
	SpaceName             string
	SpaceDescription      string
	PollInterval          time.Duration
	HeartbeatInterval     time.Duration
	HeartbeatContextLimit int
	Prompt                string
	Intent                string
	Skills                []string
	Network               map[string]any
	Logger                telemetry.Logger
}

type Runner struct {
	cfg       Config
	processed map[string]struct{}
}

func New(cfg Config) *Runner {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.HeartbeatContextLimit <= 0 {
		cfg.HeartbeatContextLimit = 50
	}
	if cfg.NodeName == "" {
		cfg.NodeName = "aiscan-loop"
	}
	if cfg.SpaceDescription == "" {
		cfg.SpaceDescription = "aiscan loop worker"
	}
	if cfg.Logger == nil {
		cfg.Logger = telemetry.NopLogger()
	}
	return &Runner{cfg: cfg, processed: make(map[string]struct{})}
}

func (r *Runner) Run(ctx context.Context) error {
	if r.cfg.Client == nil {
		return fmt.Errorf("acp client is required")
	}
	if r.cfg.Provider == nil {
		return fmt.Errorf("agent provider is required")
	}
	if r.cfg.Tools == nil {
		r.cfg.Tools = command.NewRegistry()
	}
	if r.cfg.Client.NodeID() == "" {
		node, err := r.cfg.Client.RegisterNode(ctx, r.cfg.NodeName, map[string]any{"client": "aiscan-loop"})
		if err != nil {
			return err
		}
		r.cfg.Logger.Infof("acp node=%s name=%q status=registered", node.ID, node.Name)
	}
	if strings.TrimSpace(r.cfg.SpaceName) == "" {
		return fmt.Errorf("loop space name is required")
	}
	space, err := r.cfg.Client.Space(ctx, r.cfg.SpaceName, r.cfg.SpaceDescription)
	if err != nil {
		return err
	}
	r.cfg.Logger.Importantf("loop status=listening space=%s name=%q node=%s", space.ID, space.Name, r.cfg.Client.NodeID())

	if err := r.announceProfile(ctx, space); err != nil {
		return err
	}

	if err := r.catchUp(ctx, space.ID); err != nil {
		return err
	}

	messages, errs, cancel, err := r.cfg.Client.Subscribe(ctx, space.ID)
	if err != nil {
		return err
	}
	defer cancel()

	ticker := time.NewTicker(r.cfg.PollInterval)
	defer ticker.Stop()
	var heartbeat *time.Ticker
	if r.cfg.HeartbeatInterval > 0 {
		heartbeat = time.NewTicker(r.cfg.HeartbeatInterval)
		defer heartbeat.Stop()
		r.cfg.Logger.Importantf("loop heartbeat=enabled interval=%s context_limit=%d", r.cfg.HeartbeatInterval, r.cfg.HeartbeatContextLimit)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-errs:
			if ok && err != nil {
				return err
			}
		case msg, ok := <-messages:
			if !ok {
				return nil
			}
			if err := r.handleMessage(ctx, space.ID, msg); err != nil {
				r.cfg.Logger.Warnf("loop message failed: %s", err)
			}
		case <-ticker.C:
			if err := r.catchUp(ctx, space.ID); err != nil {
				r.cfg.Logger.Warnf("loop catch-up failed: %s", err)
			}
		case <-heartbeatC(heartbeat):
			if err := r.runHeartbeat(ctx, space); err != nil {
				r.cfg.Logger.Warnf("loop heartbeat failed: %s", err)
			}
		}
	}
}

func (r *Runner) announceProfile(ctx context.Context, space ioa.SpaceInfo) error {
	content := map[string]any{
		"type":        "node_profile",
		"node_id":     r.cfg.Client.NodeID(),
		"node_name":   r.cfg.NodeName,
		"space_id":    space.ID,
		"space_name":  space.Name,
		"description": r.cfg.SpaceDescription,
		"prompt":      strings.TrimSpace(r.cfg.Prompt),
		"intent":      strings.TrimSpace(r.cfg.Intent),
		"skills":      cleanStrings(r.cfg.Skills),
		"network":     r.networkProfile(),
		"created_at":  time.Now().UTC().Format(time.RFC3339),
	}
	_, err := r.cfg.Client.Send(ctx, space.ID, ioa.SendMessage{Content: content})
	return err
}

func (r *Runner) networkProfile() map[string]any {
	if r.cfg.Network != nil {
		return r.cfg.Network
	}
	return localNetworkProfile()
}

func (r *Runner) catchUp(ctx context.Context, spaceID string) error {
	messages, err := r.cfg.Client.Read(ctx, spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if err := r.handleMessage(ctx, spaceID, msg); err != nil {
			r.cfg.Logger.Warnf("loop catch-up message failed: %s", err)
		}
	}
	return nil
}

func (r *Runner) runHeartbeat(ctx context.Context, space ioa.SpaceInfo) error {
	messages, err := r.cfg.Client.Read(ctx, space.ID, ioa.ReadOptions{All: true, Limit: r.cfg.HeartbeatContextLimit})
	if err != nil {
		return err
	}
	r.cfg.Logger.Importantf("loop heartbeat=running space=%s", space.ID)
	started, err := r.cfg.Client.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"type":             "heartbeat",
			"status":           "started",
			"node_id":          r.cfg.Client.NodeID(),
			"node_name":        r.cfg.NodeName,
			"space_id":         space.ID,
			"space_name":       space.Name,
			"interval_seconds": int(r.cfg.HeartbeatInterval.Seconds()),
			"created_at":       time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		return err
	}

	task := r.heartbeatPrompt(space, messages)
	result, runErr := agent.Run(ctx, task, r.cfg.Tools,
		agent.WithProvider(r.cfg.Provider),
		agent.WithSystemPrompt(r.cfg.SystemPrompt),
		agent.WithModel(r.cfg.Model),
		agent.WithStream(r.cfg.Stream),
		agent.WithLogger(r.cfg.Logger),
	)
	content := map[string]any{
		"type":         "heartbeat_result",
		"status":       "done",
		"output":       result,
		"node_id":      r.cfg.Client.NodeID(),
		"node_name":    r.cfg.NodeName,
		"space_id":     space.ID,
		"space_name":   space.Name,
		"completed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if runErr != nil {
		content["status"] = "error"
		content["error"] = runErr.Error()
	}
	_, sendErr := r.cfg.Client.Send(ctx, space.ID, ioa.SendMessage{
		Content: content,
		Refs:    &ioa.Ref{Messages: []string{started.ID}},
	})
	if runErr != nil {
		return runErr
	}
	if sendErr == nil {
		r.cfg.Logger.Importantf("loop heartbeat=completed space=%s", space.ID)
	}
	return sendErr
}

func (r *Runner) heartbeatPrompt(space ioa.SpaceInfo, messages []ioa.Message) string {
	contextJSON, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		contextJSON = []byte("[]")
	}
	intent := strings.TrimSpace(r.cfg.Prompt)
	if intent == "" {
		intent = strings.TrimSpace(r.cfg.Intent)
	}
	if intent == "" {
		intent = "No explicit worker intent was configured."
	}
	return fmt.Sprintf(`This is an IOA heartbeat turn for an aiscan loop worker.

Space:
- id: %s
- name: %s

This node:
- id: %s
- name: %s
- intent: %s
- skills: %s

Review the recent IOA context below and decide the next useful step.
Use IOA tools when you need to read more context, send coordination messages, or assign tasks to other nodes.
If this worker should act now, use the available local tools directly in this heartbeat turn.
If no action is needed, say that briefly and do not repeat completed work.
Do not send heartbeat, status, result, or node_profile messages as new tasks.
When creating IOA tasks for other nodes, use content like {"type":"task","task":"..."} and target refs.nodes when a specific node should handle it.

Recent IOA messages, oldest to newest:
%s`, space.ID, space.Name, r.cfg.Client.NodeID(), r.cfg.NodeName, intent, strings.Join(cleanStrings(r.cfg.Skills), ", "), string(contextJSON))
}

func (r *Runner) handleMessage(ctx context.Context, spaceID string, msg ioa.Message) error {
	task, ok := taskFromMessage(msg)
	if !ok {
		return nil
	}
	if msg.Sender == r.cfg.Client.NodeID() {
		return nil
	}
	if !isTaskForNode(msg, r.cfg.Client.NodeID()) {
		return nil
	}
	if !r.markProcessed(msg.ID) {
		return nil
	}
	r.cfg.Logger.Importantf("loop task=received message=%s", msg.ID)

	started, err := r.cfg.Client.Send(ctx, spaceID, ioa.SendMessage{
		Content: map[string]any{
			"type":    "status",
			"status":  "started",
			"task":    task,
			"node_id": r.cfg.Client.NodeID(),
		},
		Refs: &ioa.Ref{Messages: []string{msg.ID}},
	})
	if err != nil {
		return err
	}

	result, runErr := agent.Run(ctx, task, r.cfg.Tools,
		agent.WithProvider(r.cfg.Provider),
		agent.WithSystemPrompt(r.cfg.SystemPrompt),
		agent.WithModel(r.cfg.Model),
		agent.WithStream(r.cfg.Stream),
		agent.WithLogger(r.cfg.Logger),
	)
	content := map[string]any{
		"type":    "result",
		"task":    task,
		"output":  result,
		"node_id": r.cfg.Client.NodeID(),
	}
	if runErr != nil {
		content["error"] = runErr.Error()
		content["status"] = "error"
	} else {
		content["status"] = "done"
	}
	_, sendErr := r.cfg.Client.Send(ctx, spaceID, ioa.SendMessage{
		Content: content,
		Refs:    &ioa.Ref{Messages: []string{msg.ID, started.ID}},
	})
	if runErr != nil {
		return runErr
	}
	return sendErr
}

func (r *Runner) markProcessed(messageID string) bool {
	if _, ok := r.processed[messageID]; ok {
		return false
	}
	r.processed[messageID] = struct{}{}
	return true
}

func isTaskForNode(msg ioa.Message, nodeID string) bool {
	if len(msg.Refs.Nodes) == 0 {
		return len(msg.Refs.Messages) == 0
	}
	return slices.Contains(msg.Refs.Nodes, nodeID)
}

func taskFromMessage(msg ioa.Message) (string, bool) {
	if typ, ok := msg.Content["type"].(string); ok && typ != "" && typ != "task" {
		return "", false
	}
	if value, ok := msg.Content["task"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	if value, ok := msg.Content["prompt"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	if typ, _ := msg.Content["type"].(string); typ == "task" {
		if value, ok := msg.Content["content"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

func heartbeatC(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}

func cleanStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func localNetworkProfile() map[string]any {
	hostname, _ := os.Hostname()
	profile := map[string]any{
		"hostname":   hostname,
		"interfaces": []map[string]any{},
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		profile["error"] = err.Error()
		return profile
	}
	items := make([]map[string]any, 0, len(interfaces))
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		addresses := make([]string, 0, len(addrs))
		for _, addr := range addrs {
			addresses = append(addresses, addr.String())
		}
		items = append(items, map[string]any{
			"name":      iface.Name,
			"flags":     iface.Flags.String(),
			"addresses": addresses,
		})
	}
	profile["interfaces"] = items
	return profile
}
