package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
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

func NopLogger() Logger                     { return nopLogger{} }
func (nopLogger) Debugf(string, ...any)     {}
func (nopLogger) Infof(string, ...any)      {}
func (nopLogger) Warnf(string, ...any)      {}
func (nopLogger) Errorf(string, ...any)     {}
func (nopLogger) Importantf(string, ...any) {}

type Task struct {
	MessageID string
	Sender    string
	Content   string
	Targets   []string
	Meta      map[string]any
	Refs      ioa.Ref
}

// PeerMessage is a non-task swarm message arriving during an active task.
// It carries enough context for the consumer to render a human-readable line
// for the LLM and to decide whether to act on refs/targets.
type PeerMessage struct {
	MessageID  string
	Sender     string
	Content    string
	RawContent map[string]any
	Targets    []string
	Meta       map[string]any
	Refs       ioa.Ref
}

func (t Task) Prompt() string {
	var sb strings.Builder
	sb.WriteString(t.Content)
	if len(t.Targets) > 0 {
		sb.WriteString("\n\nTargets:\n")
		for _, tgt := range t.Targets {
			sb.WriteString("- ")
			sb.WriteString(tgt)
			sb.WriteByte('\n')
		}
	}
	if len(t.Meta) > 0 {
		skip := true
		for k := range t.Meta {
			if k != "kind" {
				skip = false
				break
			}
		}
		if !skip {
			if data, err := json.MarshalIndent(t.Meta, "", "  "); err == nil {
				sb.WriteString("\nContext:\n")
				sb.Write(data)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

type TaskHandler func(ctx context.Context, task Task) (string, error)

type HeartbeatFunc func(ctx context.Context, prompt string) (string, error)

type CronTask struct {
	Name         string
	Interval     time.Duration
	Prompt       string
	ContextLimit int
}

type NodeConfig struct {
	Client                ioaclient.StreamAPI
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
	OnTask                TaskHandler
	OnPeer                func(PeerMessage) bool
	OnHeartbeat           HeartbeatFunc
	Logger                Logger
}

// Node coordinates a swarm worker against an IOA space.
//
// Message tracking uses three orthogonal concepts:
//
//   - historical: the snapshot of message IDs that already existed in the
//     space when this node started. These are the AI's *past context* —
//     briefing material, not new intent. The set is populated once by
//     markHistorical (called from markExisting) and is frozen thereafter.
//
//   - dispatched: message IDs we've already routed during this session.
//     Pure runtime dedup. The subscribe SSE channel and the catchUp HTTP
//     poll can deliver the same message in the rare overlap window between
//     ticks, and this set prevents double-firing.
//
//   - lastSeenID: a single string cursor advanced by catchUp to the last
//     consecutively-handled message in each poll response. catchUp passes
//     this as ioa.ReadOptions.After so the server skips messages we've
//     already handled. It's a network optimization; correctness still
//     depends on the dispatched set for SSE/catchUp race protection.
//     The cursor stops at any "deferred" message (e.g. peer buffer full)
//     so the next poll re-fetches and retries it.
type Node struct {
	cfg           NodeConfig
	mu            sync.Mutex
	historical    map[string]struct{}
	dispatched    map[string]struct{}
	lastSeenID    string
	rootMessageID string
	spaceID       string
	spaceName     string
	crons         *cronManager
	cronCh        chan CronTask
	runCtx        context.Context

	// pending holds task dispatches that arrived while another task was
	// running. The Run goroutine owns this slice (routeIncoming and the
	// activeDone case are both executed by Run), so no lock is required.
	pending []pendingTask
}

type pendingTask struct {
	msg ioa.Message
	sm  SwarmMessage
}

type activeTask struct {
	messageID string
	done      chan taskResult
}

type taskResult struct {
	messageID string
	result    string
	err       error
}

const defaultPeerBufferSize = 64

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
	n := &Node{
		cfg:        cfg,
		historical: make(map[string]struct{}),
		dispatched: make(map[string]struct{}),
		cronCh:     make(chan CronTask, 4),
	}
	n.crons = newCronManager(n.cronCh, n.runCron)
	return n
}

func (n *Node) AddCron(task CronTask) error {
	if task.ContextLimit <= 0 {
		task.ContextLimit = n.cfg.HeartbeatContextLimit
	}
	if n.runCtx == nil {
		return fmt.Errorf("node not running")
	}
	n.cfg.Logger.Importantf("swarm cron=%s interval=%s created", task.Name, task.Interval)
	return n.crons.Add(n.runCtx, task)
}

func (n *Node) RemoveCron(name string) error {
	n.cfg.Logger.Importantf("swarm cron=%s deleted", name)
	return n.crons.Remove(name)
}

func (n *Node) ListCrons() []CronTask {
	return n.crons.List()
}

func (n *Node) RootMessageID() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.rootMessageID
}
func (n *Node) NodeID() string  { return n.cfg.Client.NodeID() }
func (n *Node) SpaceID() string { return n.spaceID }

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

	n.runCtx = ctx

	var active *activeTask

	if n.cfg.HeartbeatInterval > 0 && n.cfg.OnHeartbeat != nil {
		if err := n.markExisting(ctx); err != nil {
			return err
		}
		heartbeat := CronTask{
			Name:         "heartbeat",
			Interval:     n.cfg.HeartbeatInterval,
			Prompt:       n.cfg.Prompt,
			ContextLimit: n.cfg.HeartbeatContextLimit,
		}
		if err := n.runCron(ctx, heartbeat); err != nil {
			n.cfg.Logger.Warnf("swarm heartbeat init failed: %s", err)
		}
		_ = n.crons.Add(ctx, heartbeat)
	} else {
		if err := n.catchUp(ctx, &active); err != nil {
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
	// activeDone returns the current task's done channel, or a nil channel if
	// idle. A nil channel in a select case blocks forever, so the select only
	// races against the done channel when a task is in flight.
	activeDone := func() <-chan taskResult {
		if active == nil {
			return nil
		}
		return active.done
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
			// SSE caller ignores the handled bool — watermark is owned by
			// catchUp. A deferred message here will be retried by the next
			// catchUp tick because the watermark stays behind it.
			if _, err := n.routeIncoming(ctx, msg, &active); err != nil {
				n.cfg.Logger.Warnf("swarm message failed: %s", err)
			}
		case <-ticker.C:
			if err := n.catchUp(ctx, &active); err != nil {
				n.cfg.Logger.Warnf("swarm catch-up failed: %s", err)
			}
		case cron := <-n.cronCh:
			if n.cfg.OnHeartbeat != nil {
				// Heartbeats invoke a full LLM turn-loop that can take minutes.
				// Running it inline would block the select — no message
				// routing, no peer forwarding, no task completion handling —
				// for the duration. Run in a goroutine so the loop keeps
				// servicing the space.
				cronCopy := cron
				go func() {
					if err := n.runCron(ctx, cronCopy); err != nil {
						n.cfg.Logger.Warnf("swarm cron=%s failed: %s", cronCopy.Name, err)
					}
				}()
			}
		case res := <-activeDone():
			if err := n.completeTask(ctx, res); err != nil {
				n.cfg.Logger.Warnf("swarm task=report failed: %s", err)
			}
			active = nil
			// Drain the pending queue: tasks that arrived while we were busy
			// were stashed in n.pending instead of being dropped. Start the
			// next one immediately so dispatchers don't lose work.
			for len(n.pending) > 0 {
				next := n.pending[0]
				n.pending = n.pending[1:]
				n.cfg.Logger.Importantf("swarm task=dequeued msg=%s queue_depth=%d",
					next.msg.ID, len(n.pending))
				state, err := n.startTask(ctx, next.msg, next.sm)
				if err != nil {
					n.cfg.Logger.Warnf("swarm task=dequeue-start failed msg=%s: %s",
						next.msg.ID, err)
					continue
				}
				active = state
				break
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
	n.mu.Lock()
	n.rootMessageID = sent.ID
	rootID := n.rootMessageID
	n.mu.Unlock()
	n.cfg.Logger.Infof("swarm root_message=%s node=%s", rootID, n.cfg.Client.NodeID())
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

// catchUp polls the space for messages we haven't seen yet, as a fallback
// against SSE drops or buffer overflow. The After cursor restricts the
// server response to messages strictly newer than our watermark, so we
// don't pull the entire history every PollInterval.
//
// Watermark advances to the last consecutively-handled message in the
// response. If routeIncoming defers a message (peer buffer full), the
// watermark stops there so the next poll re-fetches and retries it.
func (n *Node) catchUp(ctx context.Context, active **activeTask) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{
		All:   true,
		After: n.watermark(),
	})
	if err != nil {
		return err
	}
	var lastHandled string
	deferred := false
	for _, msg := range messages {
		handled, routeErr := n.routeIncoming(ctx, msg, active)
		if routeErr != nil {
			n.cfg.Logger.Warnf("swarm catch-up message failed: %s", routeErr)
		}
		if !handled {
			deferred = true
		}
		if !deferred {
			lastHandled = msg.ID
		}
	}
	if lastHandled != "" {
		n.advanceWatermark(lastHandled)
	}
	return nil
}

// markExisting snapshots the space at startup: every message already there
// becomes part of the AI's past-context briefing. Subsequent messages are
// fresh intent, not historical.
func (n *Node) markExisting(ctx context.Context) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		return err
	}
	for _, msg := range messages {
		n.markHistorical(msg.ID)
	}
	// Seed the watermark to the tail of the snapshot so catchUp's first
	// poll only asks for messages that arrived after we joined.
	if len(messages) > 0 {
		n.mu.Lock()
		n.lastSeenID = messages[len(messages)-1].ID
		n.mu.Unlock()
	}
	return nil
}

func (n *Node) runCron(ctx context.Context, cron CronTask) error {
	messages, err := n.cfg.Client.Read(ctx, n.spaceID, ioa.ReadOptions{All: true, Limit: cron.ContextLimit})
	if err != nil {
		return err
	}
	n.cfg.Logger.Importantf("swarm cron=%s running space=%s", cron.Name, n.spaceID)

	prompt := n.cronPrompt(cron, messages)
	result, runErr := n.cfg.OnHeartbeat(ctx, prompt)

	report := SwarmMessage{Content: result}
	if runErr != nil {
		report.Content = fmt.Sprintf("Cron %s error: %s", cron.Name, runErr.Error())
	}
	_, sendErr := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(report),
	})
	if runErr != nil {
		return runErr
	}
	if sendErr == nil {
		n.cfg.Logger.Importantf("swarm cron=%s completed space=%s", cron.Name, n.spaceID)
	}
	return sendErr
}

func (n *Node) cronPrompt(cron CronTask, messages []ioa.Message) string {
	contextJSON, err := json.MarshalIndent(messages, "", "  ")
	if err != nil {
		contextJSON = []byte("[]")
	}
	intent := strings.TrimSpace(cron.Prompt)
	if intent == "" {
		intent = strings.TrimSpace(n.cfg.Prompt)
	}
	if intent == "" {
		intent = strings.TrimSpace(n.cfg.Intent)
	}
	if intent == "" {
		intent = "No explicit worker intent was configured."
	}
	return fmt.Sprintf(`This is a Swarm heartbeat turn.

Cron task: %s (every %s)

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

Before sending IOA messages or dispatching tasks to other nodes, read the ioa skill (aiscan://skills/ioa/SKILL.md) for the required message format.

Recent messages (oldest to newest):
%s`, cron.Name, cron.Interval, n.spaceID, n.spaceName, n.cfg.Client.NodeID(), n.cfg.NodeName, intent, strings.Join(cleanStrings(n.cfg.Skills), ", "), string(contextJSON))
}

// routeIncoming filters an IOA message and either starts a new task, forwards
// the message into the currently-running task's peer channel, or drops it.
//
// Routing rules:
//   - Messages we sent ourselves: skip (no echo).
//   - Profile (node_profile) messages: skip — they advertise capabilities, not work.
//   - Explicit task messages (meta.kind == "task_dispatch") start a task if
//     idle, otherwise log and skip (single-task model per node).
//   - Legacy task messages (directed to us, addressed to our profile root, or a
//     bare broadcast) only start a task while idle.
//   - While a task is active, non-explicit messages are treated as peer chatter
//     and forwarded to the active task's Peers channel so the agent's next turn
//     can see them.
// routeIncoming filters an IOA message and either starts a task, queues it,
// forwards as peer chatter, or skips it.
//
// Returns (handled, err) where handled=true means we've made a final
// decision about this message — catchUp can advance its watermark past it.
// handled=false means the message couldn't be processed now and needs to
// stay re-fetchable (currently only: peer buffer full while a task is
// running). On err, handled is true: the error came from Send-ing the
// "Accepted task" ack, not from the routing decision itself.
func (n *Node) routeIncoming(ctx context.Context, msg ioa.Message, active **activeTask) (bool, error) {
	sm, ok := swarmFromIOA(msg)
	if isProfileMessage(msg, sm) {
		return true, nil
	}
	if msg.Sender == n.cfg.Client.NodeID() {
		return true, nil
	}

	nodeID := n.cfg.Client.NodeID()
	if len(msg.Refs.Nodes) > 0 && !slices.Contains(msg.Refs.Nodes, nodeID) {
		// Addressed to someone else — not our intent. Record as dispatched
		// so SSE re-delivery short-circuits.
		n.markDispatched(msg.ID)
		return true, nil
	}

	// isKnown covers two cases: (a) historical, set once at startup — these
	// are AI's past context, not new intent; (b) dispatched, set when we
	// routed this message earlier in the session. Either way we've already
	// accounted for it.
	if n.isKnown(msg.ID) {
		return true, nil
	}

	n.mu.Lock()
	rootID := n.rootMessageID
	n.mu.Unlock()

	isActive := *active != nil
	if ok && isTaskMessage(msg, sm, nodeID, rootID, isActive) {
		if *active != nil {
			// Queue instead of dropping. The Run goroutine will dequeue and
			// start this task as soon as the current one completes.
			n.markDispatched(msg.ID)
			n.pending = append(n.pending, pendingTask{msg: msg, sm: sm})
			n.cfg.Logger.Infof("swarm task=queued msg=%s active=%s queue_depth=%d",
				msg.ID, (*active).messageID, len(n.pending))
			return true, nil
		}
		n.markDispatched(msg.ID)
		state, err := n.startTask(ctx, msg, sm)
		if err != nil {
			return true, err
		}
		*active = state
		return true, nil
	}

	if *active != nil {
		peer := peerMessageFromIOA(msg, sm)
		if n.cfg.OnPeer != nil && n.cfg.OnPeer(peer) {
			n.markDispatched(msg.ID)
			n.cfg.Logger.Debugf("swarm peer=forwarded msg=%s sender=%s active=%s",
				msg.ID, msg.Sender, (*active).messageID)
			return true, nil
		}
		n.cfg.Logger.Warnf("swarm peer=deferred (buffer full) msg=%s active=%s",
			msg.ID, (*active).messageID)
		return false, nil
	}

	// No consumer (idle node, not a task-start message). Decision made:
	// ignore. Mark dispatched so SSE re-delivery short-circuits.
	n.markDispatched(msg.ID)
	return true, nil
}

// startTask sends the "Accepted task" acknowledgement, allocates the peer
// channel + done channel, and launches OnTask in a goroutine so the main select
// loop can keep routing peer messages.
func (n *Node) startTask(ctx context.Context, msg ioa.Message, sm SwarmMessage) (*activeTask, error) {
	n.cfg.Logger.Importantf("swarm task=received message=%s", msg.ID)
	running := SwarmMessage{Content: fmt.Sprintf("Accepted task. Executing: %s", truncate(sm.Content, 100))}
	if _, err := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(running),
		Refs:    &ioa.Ref{Messages: []string{msg.ID}},
	}); err != nil {
		return nil, err
	}

	done := make(chan taskResult, 1)
	state := &activeTask{
		messageID: msg.ID,
		done:      done,
	}

	task := Task{
		MessageID: msg.ID,
		Sender:    msg.Sender,
		Content:   sm.Content,
		Targets:   sm.Targets,
		Meta:      sm.Meta,
		Refs:      msg.Refs,
	}

	go func() {
		result, err := n.cfg.OnTask(ctx, task)
		done <- taskResult{messageID: msg.ID, result: result, err: err}
	}()

	return state, nil
}

// completeTask sends the final result / error report into the space.
func (n *Node) completeTask(ctx context.Context, res taskResult) error {
	report := SwarmMessage{Content: res.result}
	if res.err != nil {
		report.Content = fmt.Sprintf("Error: %s\n\nPartial output:\n%s", res.err.Error(), res.result)
	}
	_, sendErr := n.cfg.Client.Send(ctx, n.spaceID, ioa.SendMessage{
		Content: swarmContent(report),
		Refs:    &ioa.Ref{Messages: []string{res.messageID}},
	})
	if res.err != nil {
		n.cfg.Logger.Warnf("swarm task=failed message=%s error=%s", res.messageID, res.err)
		if sendErr != nil {
			return sendErr
		}
		return res.err
	}
	if sendErr == nil {
		n.cfg.Logger.Importantf("swarm task=completed message=%s", res.messageID)
	}
	return sendErr
}

// markHistorical records a message ID as part of the AI's past-context
// briefing. Called only during startup (markExisting). The map is frozen
// after Run begins steady-state operation.
func (n *Node) markHistorical(messageID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.historical[messageID] = struct{}{}
}

// isKnown reports whether we've already accounted for this message — either
// as past context (historical) or as something we've already routed in this
// session (dispatched).
func (n *Node) isKnown(messageID string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.historical[messageID]; ok {
		return true
	}
	_, ok := n.dispatched[messageID]
	return ok
}

// markDispatched records that we've routed this message in the current
// session. The dispatched set is purely runtime dedup (SSE + catchUp race
// protection); it does NOT advance the watermark — that's the catchUp
// loop's job, because the watermark must advance past skipped messages
// (profile, self-sent, addressed-elsewhere) too, or catchUp will keep
// re-fetching them forever.
func (n *Node) markDispatched(messageID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.dispatched[messageID] = struct{}{}
}

// advanceWatermark moves the catchUp cursor forward. Called by catchUp
// after iterating a poll response — every message in the response has
// been considered (routed or intentionally skipped), so we don't need to
// see them again.
func (n *Node) advanceWatermark(messageID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.lastSeenID = messageID
}

// watermark returns the cursor for catchUp's After: filter. Empty string
// means "no catch-up done yet" — catchUp will fetch from the beginning,
// which is fine because historical entries get filtered by isKnown.
func (n *Node) watermark() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastSeenID
}

// isTaskMessage decides whether an incoming swarm-shaped message should start
// a task run, or instead be treated as peer chatter forwarded into the active
// task's inbox.
//
//   - meta.kind == "task_dispatch": explicit task opt-in (dispatcher convention).
//   - refs.nodes set: route only to listed nodes.
//   - refs.messages set: legacy root-message task only if our profile root is referenced.
//   - while active: only explicit task_dispatch can be a new task; everything
//     else is peer chatter for the active task.
//   - while idle: preserve legacy direct/broadcast task behavior for messages
//     with no peer kind.
func isTaskMessage(msg ioa.Message, sm SwarmMessage, nodeID, rootMessageID string, active bool) bool {
	if len(msg.Refs.Nodes) > 0 {
		if !slices.Contains(msg.Refs.Nodes, nodeID) {
			return false
		}
	}

	refsRoot := false
	if len(msg.Refs.Messages) > 0 {
		refsRoot = rootMessageID != "" && slices.Contains(msg.Refs.Messages, rootMessageID)
		if !refsRoot {
			return false
		}
	}

	kind := messageKind(msg, sm)
	if kind == "task_dispatch" {
		return true
	}
	if kind != "" || active {
		return false
	}
	return len(msg.Refs.Nodes) > 0 || refsRoot || len(msg.Refs.Messages) == 0
}

func peerMessageFromIOA(msg ioa.Message, sm SwarmMessage) PeerMessage {
	peer := PeerMessage{
		MessageID:  msg.ID,
		Sender:     msg.Sender,
		Content:    sm.Content,
		RawContent: cloneContent(msg.Content),
		Targets:    sm.Targets,
		Meta:       sm.Meta,
		Refs:       msg.Refs,
	}
	if len(peer.Targets) == 0 {
		peer.Targets = parseTargets(msg.Content["targets"])
	}
	if len(peer.Meta) == 0 {
		if meta, ok := msg.Content["meta"].(map[string]any); ok {
			peer.Meta = cloneContent(meta)
		}
	}
	return peer
}

func messageKind(msg ioa.Message, sm SwarmMessage) string {
	if kind, _ := sm.Meta["kind"].(string); kind != "" {
		return kind
	}
	if kind, _ := msg.Content["kind"].(string); kind != "" {
		return kind
	}
	if meta, ok := msg.Content["meta"].(map[string]any); ok {
		kind, _ := meta["kind"].(string)
		return kind
	}
	return ""
}

func cloneContent(content map[string]any) map[string]any {
	if len(content) == 0 {
		return nil
	}
	out := make(map[string]any, len(content))
	for k, v := range content {
		out[k] = v
	}
	return out
}

func parseTargets(raw any) []string {
	if raw == nil {
		return nil
	}
	var targets []string
	if data, err := json.Marshal(raw); err == nil {
		_ = json.Unmarshal(data, &targets)
	}
	return targets
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
