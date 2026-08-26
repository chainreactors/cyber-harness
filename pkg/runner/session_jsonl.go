package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/tui"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
)

type resumeState struct {
	SessionID      string
	Model          string
	Messages       []*aop.Message
	MessageCounter int64
}

type resumeStream struct {
	id             string
	parentID       string
	parentToolCall string
	model          string
	messages       []*aop.Message
	messageCounter int64
	order          int
	started        bool
	closedReason   string
	historyMode    types.SessionHistory_Mode
}

func loadResumeState(path string) (*resumeState, error) {
	streams := make(map[string]*resumeStream)
	seenEventIDs := make(map[string]struct{})
	order := 0
	err := output.ScanJSONL(path, func(event *aop.Event) error {
		if event.Id == "" {
			return fmt.Errorf("event in %s has no id", path)
		}
		eventKey := event.SessionId + "\x00" + event.Id
		if _, exists := seenEventIDs[eventKey]; exists {
			return fmt.Errorf("event id %s is duplicated in session %s", event.Id, event.SessionId)
		}
		seenEventIDs[eventKey] = struct{}{}
		stream := streams[event.SessionId]
		if stream == nil {
			order++
			stream = &resumeStream{id: event.SessionId, order: order}
			streams[event.SessionId] = stream
		}
		switch payload := event.Payload.(type) {
		case *aop.Event_SessionStarted:
			stream.started = true
			stream.parentID = payload.SessionStarted.ParentSessionId
			stream.parentToolCall = payload.SessionStarted.ParentToolCallId
			history, ok, err := types.GetSessionHistory(event)
			if err != nil {
				return fmt.Errorf("session %s has invalid history metadata: %w", event.SessionId, err)
			}
			if !ok || history.GetMode() == types.SessionHistory_MODE_UNSPECIFIED {
				return fmt.Errorf("session %s has no explicit history metadata", event.SessionId)
			}
			stream.historyMode = history.GetMode()
			if payload.SessionStarted.Model != "" {
				stream.model = payload.SessionStarted.Model
			}
		case *aop.Event_SessionEnded:
			stream.closedReason = payload.SessionEnded.Reason
		case *aop.Event_Message:
			if payload.Message == nil || (payload.Message.Role != "user" && payload.Message.Role != "assistant") {
				return nil
			}
			if _, command, _ := types.GetCommandDetail(event); command {
				return nil
			}
			stream.messages = append(stream.messages, proto.CloneOf(payload.Message))
			stream.messageCounter = max(stream.messageCounter, messageIDSequence(payload.Message.Id))
		case *aop.Event_ToolResult:
			if payload.ToolResult != nil {
				stream.messages = append(stream.messages, &aop.Message{
					Role: "tool", Content: []*aop.Content{{Value: &aop.Content_ToolResult{ToolResult: proto.CloneOf(payload.ToolResult)}}},
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	var selected *resumeStream
	for _, stream := range streams {
		if !stream.started || stream.parentToolCall != "" {
			continue
		}
		if len(stream.messages) == 0 && stream.parentID == "" && stream.model == "" {
			continue
		}
		if selected == nil || stream.order > selected.order {
			selected = stream
		}
	}
	if selected == nil {
		return nil, fmt.Errorf("no resumable AOP session found in %s", path)
	}
	messages, counter, err := resumeStreamMessages(selected, streams)
	if err != nil {
		return nil, err
	}
	return &resumeState{
		SessionID: selected.id, Model: selected.model, Messages: messages,
		MessageCounter: counter,
	}, nil
}

// resumeStreamMessages reconstructs the in-memory transcript without creating
// new events for inherited history. A compacted child explicitly declares a
// snapshot and supersedes its parent; all other sessions inherit their parent
// transcript and only contribute their own turn messages.
func resumeStreamMessages(selected *resumeStream, streams map[string]*resumeStream) ([]*aop.Message, int64, error) {
	if selected == nil {
		return nil, 0, nil
	}
	chain := make([]*resumeStream, 0, 4)
	seen := make(map[string]struct{})
	current := selected
	for current != nil {
		if _, ok := seen[current.id]; ok {
			return nil, 0, fmt.Errorf("session parent cycle detected at %s", current.id)
		}
		seen[current.id] = struct{}{}
		chain = append(chain, current)
		if current.historyMode == types.SessionHistory_MODE_SNAPSHOT || current.parentID == "" || current.parentToolCall != "" {
			break
		}
		// /clear and /compact deliberately reset or replace the parent context;
		// do not resurrect the discarded history when loading the file later.
		if parent := streams[current.parentID]; parent != nil {
			if parent.closedReason == string(SessionCloseCleared) || parent.closedReason == string(SessionCloseCompacted) {
				break
			}
			if !parent.started {
				return nil, 0, fmt.Errorf("session %s refers to parent %s without a session.started event", current.id, current.parentID)
			}
		} else {
			return nil, 0, fmt.Errorf("session %s refers to missing parent %s", current.id, current.parentID)
		}
		current = streams[current.parentID]
	}

	var messages []*aop.Message
	var counter int64
	for i := len(chain) - 1; i >= 0; i-- {
		stream := chain[i]
		for _, message := range stream.messages {
			if message == nil {
				continue
			}
			messages = append(messages, proto.CloneOf(message))
			counter = max(counter, messageIDSequence(message.Id))
		}
		counter = max(counter, stream.messageCounter)
	}
	return messages, counter, nil
}

func messageIDSequence(id string) int64 {
	if !strings.HasPrefix(id, "m-") {
		return 0
	}
	value, _ := strconv.ParseInt(strings.TrimPrefix(id, "m-"), 10, 64)
	return value
}

func listSavedSessions(dir string) ([]tui.SavedSession, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	var sessions []tui.SavedSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := loadResumeState(path)
		if err != nil {
			continue
		}
		updatedAt := time.Time{}
		if info, infoErr := entry.Info(); infoErr == nil {
			updatedAt = info.ModTime()
		}
		sessions = append(sessions, tui.SavedSession{
			Path: path, SessionID: state.SessionID, Model: state.Model,
			Messages: len(state.Messages), UpdatedAt: updatedAt,
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt.Equal(sessions[j].UpdatedAt) {
			return sessions[i].Path > sessions[j].Path
		}
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	return sessions, nil
}
