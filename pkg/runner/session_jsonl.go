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
}

func loadResumeState(path string) (*resumeState, error) {
	streams := make(map[string]*resumeStream)
	order := 0
	err := output.ScanJSONL(path, func(event *aop.Event) error {
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
			if payload.SessionStarted.Model != "" {
				stream.model = payload.SessionStarted.Model
			}
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
	return &resumeState{
		SessionID: selected.id, Model: selected.model, Messages: selected.messages,
		MessageCounter: selected.messageCounter,
	}, nil
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
