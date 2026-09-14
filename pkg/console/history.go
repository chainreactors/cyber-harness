package console

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

func listSavedSessions(dir string) ([]SavedSession, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	var sessions []SavedSession
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		state, err := agentext.ReadHistory(path)
		if err != nil {
			continue
		}
		updatedAt := time.Time{}
		if info, infoErr := entry.Info(); infoErr == nil {
			updatedAt = info.ModTime()
		}
		sessions = append(sessions, SavedSession{
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
