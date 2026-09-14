package console

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
)

// Admission is synchronized with Close; Runtime is the only execution queue.
func (r *AgentConsole) beginWork() (context.Context, error) {
	r.workMu.Lock()
	defer r.workMu.Unlock()
	if r.closed || r.ctx.Err() != nil {
		return nil, context.Canceled
	}
	r.active++
	r.work.Add(1)
	return r.submitCtx, nil
}
func (r *AgentConsole) endWork() {
	r.workMu.Lock()
	r.active--
	r.workMu.Unlock()
	r.work.Done()
}
func (r *AgentConsole) submitPrompt(text string, continuation bool) error {
	if !continuation && strings.TrimSpace(text) == "" {
		return nil
	}
	ctx, err := r.beginWork()
	if err != nil {
		return err
	}
	display, prompt := r.resolvePastedText(text)
	r.workMu.Lock()
	r.inputSeq++
	id := fmt.Sprintf("%s-%020d", r.inputID, r.inputSeq)
	r.previews[id] = display
	r.renderPreviewsLocked()
	r.workMu.Unlock()
	input := agentext.RunInput{TurnID: id, Continue: continuation}
	if !continuation {
		input.Content = []*aop.Content{aop.Text(prompt)}
	}
	run, err := r.session.Run(ctx, input)
	if err != nil {
		r.workMu.Lock()
		delete(r.previews, id)
		r.renderPreviewsLocked()
		r.workMu.Unlock()
		r.endWork()
		return err
	}
	go func() {
		defer r.endWork()
		_, _ = run.Wait()
	}()
	return nil
}
func (r *AgentConsole) command(line string) error {
	ctx, err := r.beginWork()
	if err != nil {
		return err
	}
	defer r.endWork()
	_, err = r.session.Command(ctx, strings.TrimSpace(line))
	return err
}
func (r *AgentConsole) Running() bool {
	r.workMu.Lock()
	defer r.workMu.Unlock()
	return r.active > 0
}

// Cancel only submissions derived from this terminal's context.
func (r *AgentConsole) InterruptCurrentRun() bool {
	r.workMu.Lock()
	defer r.workMu.Unlock()
	if r.closed || r.active == 0 {
		return false
	}
	r.submitCancel()
	r.submitCtx, r.submitCancel = context.WithCancel(r.ctx)
	return true
}
func (r *AgentConsole) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		r.workMu.Lock()
		r.closed = true
		r.submitCancel()
		r.cancel()
		r.workMu.Unlock()
		r.work.Wait()
		// The callback takes workMu; drain it before taking the lock to
		// release output. Subscription owns callback admission and lifetime.
		_ = r.subscription.Close(context.Background())
		r.workMu.Lock()
		defer r.workMu.Unlock()
		r.output.Close()
	})
}
func (r *AgentConsole) handleEvent(event *aop.Event) {
	if event == nil || event.SessionId != r.session.ID() || isSessionBootstrapEvent(event) {
		return
	}
	r.workMu.Lock()
	defer r.workMu.Unlock()
	boundary := event.GetTurnStarted() != nil || event.GetTurnEnded() != nil
	if boundary {
		delete(r.previews, event.TurnId)
	}
	r.output.HandleEvent(event)
	if boundary {
		r.renderPreviewsLocked()
	}
	if ended := event.GetTurnEnded(); ended != nil {
		pc := r.providerConfig()
		window := pc.ContextWindow
		if window <= 0 {
			window = agent.ModelContextWindow(pc.Model)
		}
		if window > 0 && int(ended.ContextTokens)*100/window >= 80 {
			r.compactContextTokens, r.compactContextWindow = int(ended.ContextTokens), window
		}
		r.refreshPromptAfterAsyncRun()
	}
}

// Previews are display text only: no operation, callback, or cancellation state.
func (r *AgentConsole) renderPreviewsLocked() {
	keys := make([]string, 0, len(r.previews))
	for id := range r.previews {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	texts := make([]string, 0, len(keys))
	for _, id := range keys {
		text := r.previews[id]
		if text == "" {
			text = "continue"
		}
		texts = append(texts, text)
	}
	r.output.SetInbox(texts)
}
