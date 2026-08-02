package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type CheckpointData struct {
	Version   int            `json:"version"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	Model     string         `json:"model,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	Messages  []*aop.Message `json:"messages"`
	// MessageCounter resumes AOP message_id allocation ("m-<n>") after restore.
	MessageCounter int64 `json:"message_counter,omitempty"`
}

type CheckpointInfo struct {
	Path      string
	CreatedAt time.Time
	UpdatedAt time.Time
	ModTime   time.Time
	Model     string
	Provider  string
	Messages  int
}

const checkpointVersion = 1

func SaveCheckpoint(dir string, data *CheckpointData) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}
	now := time.Now()
	if data.CreatedAt.IsZero() {
		data.CreatedAt = now
	}
	data.UpdatedAt = now
	data.Version = checkpointVersion
	data.Messages = sanitizeMessagesForSave(data.Messages)

	raw, err := json.MarshalIndent(checkpointJSON{
		Version:        data.Version,
		CreatedAt:      data.CreatedAt,
		UpdatedAt:      data.UpdatedAt,
		Model:          data.Model,
		Provider:       data.Provider,
		Messages:       marshalMessages(data.Messages),
		MessageCounter: data.MessageCounter,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}

	ts := now.Format("20060102-150405")
	tsPath := filepath.Join(dir, fmt.Sprintf("session-%s.json", ts))
	if err := os.WriteFile(tsPath, raw, 0o644); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	return nil
}

func LoadCheckpoint(path string) (*CheckpointData, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	var data checkpointJSON
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	messages, err := unmarshalMessages(data.Messages)
	if err != nil {
		return nil, fmt.Errorf("parse session messages: %w", err)
	}
	return &CheckpointData{
		Version:        data.Version,
		CreatedAt:      data.CreatedAt,
		UpdatedAt:      data.UpdatedAt,
		Model:          data.Model,
		Provider:       data.Provider,
		Messages:       messages,
		MessageCounter: data.MessageCounter,
	}, nil
}

// checkpointJSON is the on-disk envelope. Messages are stored as proto-JSON so
// the file format mirrors the AOP truth instead of a vendor shape.
type checkpointJSON struct {
	Version        int               `json:"version"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
	Model          string            `json:"model,omitempty"`
	Provider       string            `json:"provider,omitempty"`
	Messages       []json.RawMessage `json:"messages"`
	MessageCounter int64             `json:"message_counter,omitempty"`
}

func marshalMessages(messages []*aop.Message) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(messages))
	for _, m := range messages {
		raw, err := protojson.Marshal(m)
		if err != nil {
			continue
		}
		out = append(out, raw)
	}
	return out
}

func unmarshalMessages(raw []json.RawMessage) ([]*aop.Message, error) {
	out := make([]*aop.Message, 0, len(raw))
	for _, data := range raw {
		msg := new(aop.Message)
		if err := protojson.Unmarshal(data, msg); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

type checkpointMeta struct {
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Model     string            `json:"model,omitempty"`
	Provider  string            `json:"provider,omitempty"`
	Messages  []json.RawMessage `json:"messages"`
}

func ListCheckpoints(dir string) ([]CheckpointInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session dir: %w", err)
	}
	sessions := make([]CheckpointInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if matched, _ := filepath.Match("session-*.json", entry.Name()); !matched {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var meta checkpointMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			continue
		}
		fi, _ := entry.Info()
		modTime := time.Time{}
		if fi != nil {
			modTime = fi.ModTime()
		}
		sessions = append(sessions, CheckpointInfo{
			Path:      path,
			CreatedAt: meta.CreatedAt,
			UpdatedAt: meta.UpdatedAt,
			ModTime:   modTime,
			Model:     meta.Model,
			Provider:  meta.Provider,
			Messages:  len(meta.Messages),
		})
	}
	sort.Slice(sessions, func(i, j int) bool {
		left := sessions[i].SortTime()
		right := sessions[j].SortTime()
		if left.Equal(right) {
			return sessions[i].Path > sessions[j].Path
		}
		return left.After(right)
	})
	return sessions, nil
}

func (s CheckpointInfo) SortTime() time.Time {
	switch {
	case !s.UpdatedAt.IsZero():
		return s.UpdatedAt
	case !s.CreatedAt.IsZero():
		return s.CreatedAt
	default:
		return s.ModTime
	}
}

// sanitizeMessagesForSave strips binary media parts before persisting: an
// image is re-fetchable context, not history worth 20 MiB of JSON. Text and
// tool call/result structure is preserved.
func sanitizeMessagesForSave(messages []*aop.Message) []*aop.Message {
	out := make([]*aop.Message, len(messages))
	for i, m := range messages {
		hasMedia := false
		for _, part := range m.Content {
			if part.GetMedia() != nil {
				hasMedia = true
				break
			}
		}
		if !hasMedia {
			out[i] = m
			continue
		}
		filtered := make([]*aop.Content, 0, len(m.Content))
		for _, part := range m.Content {
			if part.GetMedia() == nil {
				filtered = append(filtered, part)
			}
		}
		cp := proto.CloneOf(m)
		cp.Content = filtered
		out[i] = cp
	}
	return out
}
