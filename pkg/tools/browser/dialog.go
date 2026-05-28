//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// DialogEvent records a captured JS dialog (alert, confirm, prompt).
type DialogEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	URL     string `json:"url"`
	Time    string `json:"time"`
}

// execDialog dispatches --arm / --check / --disarm.
func (c *Command) execDialog(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("browser dialog: usage: browser dialog <session> --arm|--check|--disarm")
	}
	sessName := args[0]
	flag := args[1]

	sess, err := c.getSession(sessName)
	if err != nil {
		return "", err
	}

	switch flag {
	case "--arm":
		return dialogArm(ctx, sess)
	case "--check":
		return dialogCheck(sess)
	case "--disarm":
		return dialogDisarm(sess)
	default:
		return "", fmt.Errorf("browser dialog: unknown flag %q (expected --arm, --check, or --disarm)", flag)
	}
}

func dialogArm(ctx context.Context, sess *Session) (string, error) {
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		sess.dialogMu.Lock()
		defer sess.dialogMu.Unlock()

		if sess.dialogArmed {
			return fmt.Sprintf("Dialog listener already armed on session %q", sess.Name), nil
		}

		listenCtx, cancel := context.WithCancel(context.Background())
		sess.dialogCancel = cancel
		sess.dialogArmed = true
		sess.dialogEvents = nil

		// Enable page domain to receive dialog events.
		if err := (proto.PageEnable{}).Call(page); err != nil {
			sess.dialogCancel = nil
			sess.dialogArmed = false
			cancel()
			return "", fmt.Errorf("browser dialog: enable page events: %w", err)
		}

		listenerPage := sess.Page.Context(listenCtx)
		go listenerPage.EachEvent(func(e *proto.PageJavascriptDialogOpening) {
			ev := DialogEvent{
				Type:    string(e.Type),
				Message: e.Message,
				URL:     e.URL,
				Time:    time.Now().Format(time.RFC3339),
			}
			sess.dialogMu.Lock()
			sess.dialogEvents = append(sess.dialogEvents, ev)
			sess.dialogMu.Unlock()

			// Auto-accept so the page doesn't hang.
			_ = proto.PageHandleJavaScriptDialog{Accept: true}.Call(listenerPage)
		})()

		return fmt.Sprintf("Dialog listener armed on session %q", sess.Name), nil
	})
}

func dialogCheck(sess *Session) (string, error) {
	sess.dialogMu.Lock()
	events := append([]DialogEvent(nil), sess.dialogEvents...)
	sess.dialogEvents = nil // drain
	sess.dialogMu.Unlock()

	if len(events) == 0 {
		return "No dialogs captured", nil
	}
	data, _ := json.MarshalIndent(events, "", "  ")
	return fmt.Sprintf("Captured %d dialog(s):\n%s", len(events), string(data)), nil
}

func dialogDisarm(sess *Session) (string, error) {
	sess.dialogMu.Lock()
	defer sess.dialogMu.Unlock()

	if !sess.dialogArmed {
		return fmt.Sprintf("Dialog listener not armed on session %q", sess.Name), nil
	}

	events := append([]DialogEvent(nil), sess.dialogEvents...)
	sess.dialogEvents = nil

	if sess.dialogCancel != nil {
		sess.dialogCancel()
		sess.dialogCancel = nil
	}
	sess.dialogArmed = false

	if len(events) == 0 {
		return fmt.Sprintf("Dialog listener disarmed on session %q (no dialogs captured)", sess.Name), nil
	}
	data, _ := json.MarshalIndent(events, "", "  ")
	return fmt.Sprintf("Dialog listener disarmed on session %q - captured %d dialog(s):\n%s",
		sess.Name, len(events), string(data)), nil
}
