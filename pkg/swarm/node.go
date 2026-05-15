package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
)

type Logger interface {
	Debugf(format string, args ...any)
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
	Errorf(format string, args ...any)
	Importantf(format string, args ...any)
}

type nopLogger struct{}

func NopLogger() Logger                          { return nopLogger{} }
func (nopLogger) Debugf(string, ...any)          {}
func (nopLogger) Infof(string, ...any)           {}
func (nopLogger) Warnf(string, ...any)           {}
func (nopLogger) Errorf(string, ...any)          {}
func (nopLogger) Importantf(string, ...any)      {}

type Task struct {
	MessageID string
	Sender    string
	Content   string
	Targets   []string
	Meta      map[string]any
	Refs      ioa.Ref
}

type TaskHandler func(ctx context.Context, task Task) (string, error)

type HeartbeatFunc func(ctx context.Context, prompt string) (string, error)

type NodeConfig struct {
	Client            ioaclient.StreamAPI
	NodeName          string
	SpaceName         string
	SpaceDescription  string
	PollInterval      time.Duration
	HeartbeatInterval time.Duration
	HeartbeatContextLimit int
	Prompt            string
	Intent            string
	Skills            []string
	Network           map[string]any
	OnTask            TaskHandler
	OnHeartbeat       HeartbeatFunc
	Logger            Logger
}

type Node struct {
	cfg           NodeConfig
	processed     map[string]struct{}
	rootMessageID string
	spaceID       string
	spaceName     string
}

func NewNode(cfg NodeConfig) *Node {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.HeartbeatContextLimit <= 0 {
		cfg.HeartbeatContextLimit = 50
	}
	if cfg.NodeName == "" {
		cfg.NodeName = "aiscan-swarm"
	}
	if cfg.SpaceDescription == "" {
		cfg.SpaceDescription = "aiscan swarm worker"
	}
	if cfg.Logger == nil {
		cfg.Logger = NopLogger()
	}
	return &Node{cfg: cfg, processed: make(map[string]struct{})}
}

func (n *Node) RootMessageID() string { return n.rootMessageID }
func (n *Node) NodeID() string        { return n.cfg.Client.NodeID() }
func (n *Node) SpaceID() string       { return n.spaceID }

func (n *Node) Run(ctx context.Context) error {
	if n.cfg.Client == nil {
		return fmt.Errorf("ioa client is required")
	}
	if n.cfg.OnTask == nil {
		return fmt.Errorf("task handler is required")
	}
	if n.cfg.Client.NodeID() == "" {
		node, err := n.cfg.Client.RegisterNode(ctx, n.cfg.NodeName, map[string]any{"client": "aiscan-swarm"})
		if err != nil {
			return err
		}
		n.cfg.Logger.Infof("swarm node=%s name=%q status=registered", node.ID, node.Name)
	}
	if strings.TrimSpace(n.cfg.SpaceName) == "" {
		return fmt.Errorf("space name is required")
	}
	space, err := n.cfg.Client.Space(ctx, n.cfg.SpaceName, n.cfg.SpaceDescription)
	if err != nil {
		return err
	}
	n.spaceID = space.ID
	n.spaceName = space.Name
	n.cfg.Logger.Importantf("swarm status=listening space=%s name=%q node=%s", space.ID, space.Name, n.cfg.Client.NodeID())

	if err := n.announceProfile(ctx); err != nil {
		return err
	}

	if n.cfg.HeartbeatInterval > 0 {
		if err := n.markExisting(ctx); err != nil {
			return err
		}
		if n.cfg.OnHeartbeat != nil {
			if err := n.runHeartbeat(ctx); err != nil {
				n.cfg.Logger.Warnf("swarm heartbeat failed: %s", err)
			}
		}
	} else {
		if err := n.catchUp(ctx); err != nil {
			return err
		}
	}

	messages, errs, cancel, err := n.cfg.Client.Subscribe(ctx, n.spaceID)
	if err != nil {
		return err
	}
	defer cancel()

	ticker := time.NewTicker(n.cfg.PollInterval)
	defer ticker.Stop()
	var heartbeat *time.Ticker
	if n.cfg.HeartbeatInterval > 0 {
		heartbeat = time.NewTicker(n.cfg.HeartbeatInterval)
		defer heartbeat.Stop()
		n.cfg.Logger.Importantf("swarm heartbeat=enabled interval=%s context_limit=%d", n.cfg.HeartbeatInterval, n.cfg.HeartbeatContextLimit)
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
			if err := n.handleMessage(ctx, msg); err != nil {
				n.cfg.Logger.Warnf("swarm message failed: %s", err)
			}
		case <-ticker.C:
			if err := n.catchUp(ctx); err != nil {
				n.cfg.Logger.Warnf("swarm catch-up failed: %s", err)
			}
		case <-heartbeatC(heartbeat):
			if n.cfg.OnHeartbeat != nil {
				if err := n.runHeartbeat(ctx); err != nil {
					n.cfg.Logger.Warnf("swarm heartbeat failed: %s", err)
				}
			}
		}
	}
}

func (n *Node) announceProfile(ctx context.Context) error {
	parts := []string{fmt.Sprintf("Node %s (%s) joined the swarm.", n.cfg.NodeName, n.cfg.Client.NodeID())}
	if intent := strings.TrimSpace(n.cfg.Intent); intent != "" {
		parts = append(parts, "Intent: "+intent)
	}
	if skills := cleanStrings(n.cfg.Skills); len(skills) > 0 {
		parts = append(parts, "Skills: "+strings.Join(skills, ", "))
	}
	msg := SwarmMessage{
		Content: strings.Join(parts, "\n"),
		Meta:    n.buildMeta(),
	}
	sent, err := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{Content: swarmContent(msg)})
	if err != nil {
		return err
	}
	n.rootMessageID = sent.ID
	n.cfg.Logger.Infof("swarm root_message=%s node=%s", n.rootMessageID, n.cfg.Client.NodeID())
	return nil
}

func (n *Node) buildMeta() map[string]any {
	meta := map[string]any{
		"kind":      "node_profile",
		"node_name": n.cfg.NodeName,
	}
	if n.cfg.Network != nil {
		for k, v := range n.cfg.Network {
			meta[k] = v
		}
	} else {
		hostname, _ := os.Hostname()
		meta["hostname"] = hostname
		if addrs := localAddresses(); len(addrs) > 0 {
			meta["addresses"] = addrs
		}
	}
	if skills := cleanStrings(n.cfg.Skills); len(skills) > 0 {
		meta["capabilities"] = skills
	}
	return meta
}

func (n *Node) catchUp(ctx context.Context) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		return err
	}
	for _, msg := range messages {
		if err := n.handleMessage(ctx, msg); err != nil {
			n.cfg.Logger.Warnf("swarm catch-up message failed: %s", err)
		}
	}
	return nil
}

func (n *Node) markExisting(ctx context.Context) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		return err
	}
	for _, msg := range messages {
		n.markProcessed(msg.ID)
	}
	return nil
}

func (n *Node) runHeartbeat(ctx context.Context) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{All: true, Limit: n.cfg.HeartbeatContextLimit})
	if err != nil {
		return err
	}
	n.cfg.Logger.Importantf("swarm heartbeat=running space=%s", n.spaceID)

	prompt := n.heartbeatPrompt(messages)
	result, runErr := n.cfg.OnHeartbeat(ctx, prompt)

	report := SwarmMessage{Content: result}
	if runErr != nil {
		report.Content = fmt.Sprintf("Heartbeat error: %s", runErr.Error())
	}
	_, sendErr := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(report),
	})
	if runErr != nil {
		return runErr
	}
	if sendErr == nil {
		n.cfg.Logger.Importantf("swarm heartbeat=completed space=%s", n.spaceID)
	}
	return sendErr
}

func (n *Node) heartbeatPrompt(messages []ioa.Message) string {
	contextJSON, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		contextJSON = []byte("[]")
	}
	intent := strings.TrimSpace(n.cfg.Prompt)
	if intent == "" {
		intent = strings.TrimSpace(n.cfg.Intent)
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
%s`, n.spaceID, n.spaceName, n.cfg.Client.NodeID(), n.cfg.NodeName, intent, strings.Join(cleanStrings(n.cfg.Skills), ", "), string(contextJSON))
}

func (n *Node) handleMessage(ctx context.Context, msg ioa.Message) error {
	sm, ok := swarmFromIOA(msg)
	if !ok {
		return nil
	}
	if isProfileMessage(sm) {
		return nil
	}
	if msg.Sender == n.cfg.Client.NodeID() {
		return nil
	}
	if !isTaskForNode(msg, n.cfg.Client.NodeID(), n.rootMessageID) {
		return nil
	}
	if !n.markProcessed(msg.ID) {
		return nil
	}
	n.cfg.Logger.Importantf("swarm task=received message=%s", msg.ID)

	running := SwarmMessage{Content: fmt.Sprintf("Accepted task. Executing: %s", truncate(sm.Content, 100))}
	_, err := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(running),
		Refs:    &ioa.Ref{Messages: []string{msg.ID}},
	})
	if err != nil {
		return err
	}

	task := Task{
		MessageID: msg.ID,
		Sender:    msg.Sender,
		Content:   sm.Content,
		Targets:   sm.Targets,
		Meta:      sm.Meta,
		Refs:      msg.Refs,
	}
	result, runErr := n.cfg.OnTask(ctx, task)

	report := SwarmMessage{Content: result}
	if runErr != nil {
		report.Content = fmt.Sprintf("Error: %s\n\nPartial output:\n%s", runErr.Error(), result)
	}
	_, sendErr := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(report),
		Refs:    &ioa.Ref{Messages: []string{msg.ID}},
	})
	if runErr != nil {
		return runErr
	}
	return sendErr
}

func (n *Node) markProcessed(messageID string) bool {
	if _, ok := n.processed[messageID]; ok {
		return false
	}
	n.processed[messageID] = struct{}{}
	return true
}

func isTaskForNode(msg ioa.Message, nodeID, rootMessageID string) bool {
	if len(msg.Refs.Nodes) > 0 {
		return slices.Contains(msg.Refs.Nodes, nodeID)
	}
	if len(msg.Refs.Messages) > 0 {
		return rootMessageID != "" && slices.Contains(msg.Refs.Messages, rootMessageID)
	}
	return true
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
