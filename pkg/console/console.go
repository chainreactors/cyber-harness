package console

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	"github.com/chainreactors/aiscan/pkg/tui"
)

// Console bindings adapt runtime operations and saved sessions to the TUI.
// Keep presentation types here, out of session execution and JSONL replay.
func consoleAppInfo(rt *runtimepkg.AgentRuntime) tui.AppInfo {
	provider, providerConfig := rt.App().ProviderState()
	return tui.AppInfo{
		Provider:          provider,
		ProviderConfig:    providerConfig,
		ProviderFallbacks: rt.App().ProviderFallbacks,
		Commands:          rt.App().Commands,
		Skills:            rt.App().Skills,
		OnProviderChange:  rt.SetProvider,
		OnLoggerChange:    rt.SetLogger,
	}
}

func consoleAppInfoForSession(rt *runtimepkg.AgentRuntime, session *runtimepkg.Session) tui.AppInfo {
	info := consoleAppInfo(rt)
	info.Run = func(ctx context.Context, prompt string, continuation bool) (*agent.Result, error) {
		input := runtimepkg.RunInput{Continue: continuation}
		if !continuation {
			input.Content = []*aop.Content{aop.Text(prompt)}
		}
		run, err := session.Run(ctx, input)
		if err != nil {
			return nil, err
		}
		result, err := run.Wait()
		return &agent.Result{
			Output:        result.Output,
			Stop:          result.Stop,
			TotalUsage:    result.Usage,
			ContextTokens: result.ContextTokens,
		}, err
	}
	info.Command = func(ctx context.Context, line string) error {
		_, err := session.Command(ctx, line)
		return err
	}
	info.Resume = session.Resume
	info.ListSessions = func() ([]tui.SavedSession, error) {
		return listSavedSessions(cfg.DataSubDir("sessions"))
	}
	info.ActiveAgent = session.Agent
	info.ActiveSessionID = session.ID
	return info
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
		state, err := runtimepkg.ReadHistory(path)
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
