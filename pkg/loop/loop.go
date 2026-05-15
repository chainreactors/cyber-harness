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
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/protocol"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
)

type Config struct {
	Client       ioaclient.StreamAPI
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
		return fmt.Errorf("ioa client is required")
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
		r.cfg.Logger.Infof("ioa node=%s name=%q status=registered", node.ID, node.Name)
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

	if r.cfg.HeartbeatInterval > 0 {
		if err := r.markExisting(ctx, space.ID); err != nil {
			return err
		}
		if err := r.runHeartbeat(ctx, space); err != nil {
			r.cfg.Logger.Warnf("loop heartbeat failed: %s", err)
		}
	} else {
		if err := r.catchUp(ctx, space.ID); err != nil {
			return err
		}
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
	parts := []string{fmt.Sprintf("Node %s (%s) joined the swarm.", r.cfg.NodeName, r.cfg.Client.NodeID())}
	if intent := strings.TrimSpace(r.cfg.Intent); intent != "" {
		parts = append(parts, "Intent: "+intent)
	}
	if skills := cleanStrings(r.cfg.Skills); len(skills) > 0 {
		parts = append(parts, "Skills: "+strings.Join(skills, ", "))
	}
	msg := protocol.SwarmMessage{
		Content: strings.Join(parts, "\n"),
		Meta:    r.buildMeta(),
	}
	_, err := r.cfg.Client.Send(ctx, space.ID, ioa.SendMessage{Content: swarmContent(msg)})
	return err
}

func (r *Runner) buildMeta() map[string]any {
	meta := map[string]any{
		"kind":      "node_profile",
		"node_name": r.cfg.NodeName,
	}
	if r.cfg.Network != nil {
		for k, v := range r.cfg.Network {
			meta[k] = v
		}
	} else {
		hostname, _ := os.Hostname()
		meta["hostname"] = hostname
		if addrs := localAddresses(); len(addrs) > 0 {
			meta["addresses"] = addrs
		}
	}
	if skills := cleanStrings(r.cfg.Skills); len(skills) > 0 {
		meta["capabilities"] = skills
	}
	return meta
}

func localAddresses() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var result []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			result = append(result, addr.String())
		}
	}
	return result
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

func (r *Runner) markExisting(ctx context.Context, spaceID string) error {
	messages, err := r.cfg.Client.Read(ctx, spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		return err
	}
	for _, msg := range messages {
		r.markProcessed(msg.ID)
	}
	return nil
}

func (r *Runner) runHeartbeat(ctx context.Context, space ioa.SpaceInfo) error {
	messages, err := r.cfg.Client.Read(ctx, space.ID, ioa.ReadOptions{All: true, Limit: r.cfg.HeartbeatContextLimit})
	if err != nil {
		return err
	}
	r.cfg.Logger.Importantf("loop heartbeat=running space=%s", space.ID)

	prompt := r.heartbeatPrompt(space, messages)
	result, runErr := agent.Run(ctx, prompt, r.cfg.Tools,
		agent.WithProvider(r.cfg.Provider),
		agent.WithSystemPrompt(r.cfg.SystemPrompt),
		agent.WithModel(r.cfg.Model),
		agent.WithStream(r.cfg.Stream),
		agent.WithLogger(r.cfg.Logger),
	)

	report := protocol.SwarmMessage{Content: result}
	if runErr != nil {
		report.Content = fmt.Sprintf("Heartbeat error: %s", runErr.Error())
	}
	_, sendErr := r.cfg.Client.Send(ctx, space.ID, ioa.SendMessage{
		Content: swarmContent(report),
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
	return fmt.Sprintf(`This is a Swarm heartbeat turn.

Space:
- id: %s
- name: %s

This node:
- id: %s
- name: %s
- intent: %s
- skills: %s

Review the recent messages below and decide the next useful step.
If this worker should act now, use the available local tools directly.
If no action is needed, say that briefly and do not repeat completed work.

When sending tasks to other nodes, use content like {"content":"...", "targets":["..."]} and set refs.nodes to target a specific node.
When reporting results, set refs.messages to reference the original task message.

Recent messages (oldest to newest):
%s`, space.ID, space.Name, r.cfg.Client.NodeID(), r.cfg.NodeName, intent, strings.Join(cleanStrings(r.cfg.Skills), ", "), string(contextJSON))
}

func (r *Runner) handleMessage(ctx context.Context, spaceID string, msg ioa.Message) error {
	sm, ok := swarmFromIOA(msg)
	if !ok {
		return nil
	}
	if isProfileMessage(sm) {
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

	// Report: running
	running := protocol.SwarmMessage{Content: fmt.Sprintf("Accepted task. Executing: %s", truncate(sm.Content, 100))}
	_, err := r.cfg.Client.Send(ctx, spaceID, ioa.SendMessage{
		Content: swarmContent(running),
		Refs:    &ioa.Ref{Messages: []string{msg.ID}},
	})
	if err != nil {
		return err
	}

	result, runErr := agent.Run(ctx, sm.Content, r.cfg.Tools,
		agent.WithProvider(r.cfg.Provider),
		agent.WithSystemPrompt(r.cfg.SystemPrompt),
		agent.WithModel(r.cfg.Model),
		agent.WithStream(r.cfg.Stream),
		agent.WithLogger(r.cfg.Logger),
	)

	// Report: done or error
	report := protocol.SwarmMessage{Content: result}
	if runErr != nil {
		report.Content = fmt.Sprintf("Error: %s\n\nPartial output:\n%s", runErr.Error(), result)
	}
	_, sendErr := r.cfg.Client.Send(ctx, spaceID, ioa.SendMessage{
		Content: swarmContent(report),
		Refs:    &ioa.Ref{Messages: []string{msg.ID}},
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

// swarmFromIOA tries to parse an IOA message as a SwarmMessage (new format),
// falling back to the legacy {"type":"task","task":"..."} format.
func swarmFromIOA(msg ioa.Message) (protocol.SwarmMessage, bool) {
	if sm, ok := protocol.ParseSwarm(msg.Content); ok {
		return sm, true
	}
	return protocol.ParseLegacyTask(msg.Content)
}

func swarmContent(msg protocol.SwarmMessage) map[string]any {
	m := map[string]any{"content": msg.Content}
	if len(msg.Targets) > 0 {
		m["targets"] = msg.Targets
	}
	if len(msg.Meta) > 0 {
		m["meta"] = msg.Meta
	}
	return m
}

func isProfileMessage(msg protocol.SwarmMessage) bool {
	kind, _ := msg.Meta["kind"].(string)
	return kind == "node_profile"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
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
