//go:build browser

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/headless"
	"gopkg.in/yaml.v3"
)

// RecordedAction captures a single browser action for template codegen.
type RecordedAction struct {
	Action headless.ActionType
	Args   map[string]string
	Name   string
}

// recorder tracks actions during a session for nuclei headless template generation.
type recorder struct {
	mu      sync.Mutex
	actions []RecordedAction
	baseURL string
}

func newRecorder(baseURL string) *recorder {
	return &recorder{baseURL: baseURL}
}

func (r *recorder) record(action RecordedAction) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions = append(r.actions, action)
}

func (r *recorder) snapshot() []RecordedAction {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RecordedAction, len(r.actions))
	copy(out, r.actions)
	return out
}

func (r *recorder) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.actions)
}

// templateURL replaces the session's base URL with {{BaseURL}} for portability.
func (r *recorder) templateURL(rawURL string) string {
	if r.baseURL == "" {
		return rawURL
	}
	parsed, err := url.Parse(r.baseURL)
	if err != nil {
		return rawURL
	}
	base := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	if strings.HasPrefix(rawURL, base) {
		return "{{BaseURL}}" + rawURL[len(base):]
	}
	return rawURL
}

// generateTemplate builds a nuclei headless template from recorded actions.
func (r *recorder) generateTemplate(id, name string) *headless.Template {
	actions := r.snapshot()
	if len(actions) == 0 {
		return nil
	}

	steps := make([]*headless.Action, 0, len(actions))
	for _, ra := range actions {
		step := &headless.Action{
			ActionType: headless.ActionTypeHolder{ActionType: ra.Action},
			Data:       make(map[string]string, len(ra.Args)),
			Name:       ra.Name,
		}
		for k, v := range ra.Args {
			step.Data[k] = v
		}
		steps = append(steps, step)
	}

	return &headless.Template{
		ID: id,
		Info: headless.TemplateInfo{
			Name:     name,
			Author:   "aiscan-recorder",
			Severity: "info",
		},
		RequestsHeadless: []*headless.Request{{
			Steps: steps,
		}},
	}
}

// recordCommand maps a playwright command invocation to a nuclei headless action
// and appends it to the session's recorder. Returns true if the action was recorded.
func recordCommand(sess *Session, cmd string, args []string) bool {
	if sess.rec == nil {
		return false
	}

	var ra RecordedAction
	switch cmd {
	case "goto", "navigate":
		// goto <session> [selector] is text extraction, not navigation — skip.
		// Only record when goto is used with a URL (stateless mode, not session-bound).
		return false

	case "click":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionClick,
			Args:   selectorArgs(sel),
		}

	case "dblclick":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args: map[string]string{
				"code": fmt.Sprintf(`document.querySelector(%q).dispatchEvent(new MouseEvent('dblclick', {bubbles: true}))`, sel),
			},
		}

	case "fill":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionTextInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value}),
		}

	case "type":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionTextInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value}),
		}

	case "press":
		if len(args) < 3 {
			return false
		}
		keys := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionKeyboard,
			Args:   map[string]string{"keys": keys},
		}

	case "select-option", "select":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionSelectInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value}),
		}

	case "screenshot":
		ra = RecordedAction{
			Action: headless.ActionScreenshot,
			Args:   map[string]string{},
		}
		for i := 1; i < len(args); i++ {
			if args[i] == "--full-page" {
				ra.Args["fullpage"] = "true"
			} else if args[i] == "--output" && i+1 < len(args) {
				i++
				ra.Args["to"] = args[i]
			}
		}

	case "set-input-files":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], ",")
		ra = RecordedAction{
			Action: headless.ActionFilesInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value}),
		}

	case "evaluate", "eval":
		if len(args) < 2 {
			return false
		}
		script := strings.Join(args[1:], " ")
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args:   map[string]string{"code": script},
		}

	case "hover":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args: map[string]string{
				"code": fmt.Sprintf(`document.querySelector(%q).dispatchEvent(new MouseEvent('mouseover', {bubbles: true}))`, sel),
			},
		}

	case "wait-for", "wait":
		if len(args) < 2 {
			return false
		}
		target := strings.Join(args[1:], " ")
		switch target {
		case "--idle":
			ra = RecordedAction{
				Action: headless.ActionWaitIdle,
				Args:   map[string]string{},
			}
		case "--stable":
			ra = RecordedAction{
				Action: headless.ActionWaitStable,
				Args:   map[string]string{},
			}
		default:
			ra = RecordedAction{
				Action: headless.ActionWaitVisible,
				Args:   map[string]string{"selector": target},
			}
		}

	case "set-extra-headers":
		if len(args) < 2 {
			return false
		}
		var headers map[string]string
		if err := parseJSONMap(args[1], &headers); err != nil {
			return false
		}
		for k, v := range headers {
			sess.rec.record(RecordedAction{
				Action: headless.ActionSetHeader,
				Args:   map[string]string{"part": "request", "key": k, "value": v},
			})
		}
		return true

	case "reload":
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args:   map[string]string{"code": "window.location.reload()"},
		}

	case "go-back", "back":
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args:   map[string]string{"code": "window.history.back()"},
		}

	case "go-forward", "forward":
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args:   map[string]string{"code": "window.history.forward()"},
		}

	case "check":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionClick,
			Args:   selectorArgs(sel),
		}

	case "uncheck":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionClick,
			Args:   selectorArgs(sel),
		}

	case "tap":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionClick,
			Args:   selectorArgs(sel),
		}

	case "dispatch-event":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		eventType := args[2]
		ra = RecordedAction{
			Action: headless.ActionScript,
			Args: map[string]string{
				"code": fmt.Sprintf(`document.querySelector(%q).dispatchEvent(new Event(%q, {bubbles: true}))`, sel, eventType),
			},
		}

	case "dialog":
		if len(args) >= 2 && args[1] == "--arm" {
			ra = RecordedAction{
				Action: headless.ActionWaitDialog,
				Args:   map[string]string{},
			}
		} else {
			return false
		}

	case "text-content", "text", "inner-text":
		sel := "body"
		if len(args) >= 2 {
			sel = strings.Join(args[1:], " ")
		}
		ra = RecordedAction{
			Action: headless.ActionExtract,
			Args:   selectorArgs(sel),
			Name:   sanitizeName(sel),
		}

	case "get-attribute":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		attr := args[2]
		ra = RecordedAction{
			Action: headless.ActionExtract,
			Args: mergeMaps(selectorArgs(sel), map[string]string{
				"target":    "attribute",
				"attribute": attr,
			}),
			Name: sanitizeName(sel + "_" + attr),
		}

	case "input-value":
		if len(args) < 2 {
			return false
		}
		sel := strings.Join(args[1:], " ")
		ra = RecordedAction{
			Action: headless.ActionExtract,
			Args: mergeMaps(selectorArgs(sel), map[string]string{
				"target":    "attribute",
				"attribute": "value",
			}),
			Name: sanitizeName(sel + "_value"),
		}

	case "set-viewport":
		return false

	default:
		return false
	}

	sess.rec.record(ra)
	return true
}

// execRecord handles the `record` subcommand.
func (c *Command) execRecord(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright record: usage: playwright record <session> --dump|--save <file>|--stop|--clear")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	flag := args[1]

	switch flag {
	case "--dump":
		return recordDump(sess, "", "")

	case "--save":
		if len(args) < 3 {
			return "", fmt.Errorf("playwright record --save: output file path required")
		}
		outPath := resolvePath(c.workDir, args[2])
		id, name := "recorded-template", "Recorded browser session"
		for i := 3; i < len(args); i++ {
			switch args[i] {
			case "--id":
				if i+1 < len(args) {
					i++
					id = args[i]
				}
			case "--name":
				if i+1 < len(args) {
					i++
					name = args[i]
				}
			}
		}
		return recordSave(sess, outPath, id, name)

	case "--stop":
		if sess.rec == nil {
			return fmt.Sprintf("Session %q is not recording", sess.Name), nil
		}
		count := sess.rec.len()
		sess.rec = nil
		return fmt.Sprintf("Recording stopped on session %q (%d actions captured)", sess.Name, count), nil

	case "--start":
		if sess.rec != nil {
			return fmt.Sprintf("Session %q is already recording (%d actions)", sess.Name, sess.rec.len()), nil
		}
		baseURL := ""
		if sess.Page != nil {
			if info, infoErr := sess.Page.Info(); infoErr == nil && info != nil {
				baseURL = info.URL
			}
		}
		sess.rec = newRecorder(baseURL)
		return fmt.Sprintf("Recording started on session %q", sess.Name), nil

	case "--clear":
		if sess.rec == nil {
			return fmt.Sprintf("Session %q is not recording", sess.Name), nil
		}
		sess.rec = &recorder{baseURL: sess.rec.baseURL}
		return fmt.Sprintf("Recording cleared on session %q", sess.Name), nil

	default:
		return "", fmt.Errorf("playwright record: unknown flag %q (expected --dump, --save, --stop, --start, --clear)", flag)
	}
}

func recordDump(sess *Session, id, name string) (string, error) {
	if sess.rec == nil {
		return "", fmt.Errorf("session %q is not recording (use --record flag with open, or record --start)", sess.Name)
	}
	if sess.rec.len() == 0 {
		return "No actions recorded yet", nil
	}
	if id == "" {
		id = "recorded-template"
	}
	if name == "" {
		name = "Recorded browser session"
	}

	tmpl := sess.rec.generateTemplate(id, name)
	data, err := yaml.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("marshal template: %w", err)
	}
	return string(data), nil
}

func recordSave(sess *Session, path, id, name string) (string, error) {
	if sess.rec == nil {
		return "", fmt.Errorf("session %q is not recording", sess.Name)
	}
	if sess.rec.len() == 0 {
		return "", fmt.Errorf("no actions recorded yet")
	}

	tmpl := sess.rec.generateTemplate(id, name)
	data, err := yaml.Marshal(tmpl)
	if err != nil {
		return "", fmt.Errorf("marshal template: %w", err)
	}

	if err := os.WriteFile(path, data, 0640); err != nil {
		return "", fmt.Errorf("write template: %w", err)
	}
	return fmt.Sprintf("Template saved: %s (%d actions)", path, sess.rec.len()), nil
}

// selectorArgs converts a CSS/XPath selector string to nuclei action args.
func selectorArgs(sel string) map[string]string {
	sel = strings.TrimSpace(sel)
	if xpath, ok := strings.CutPrefix(sel, "xpath:"); ok {
		return map[string]string{"by": "xpath", "xpath": xpath}
	}
	return map[string]string{"selector": sel}
}

// extractSelector extracts a selector from args starting at the given offset.
func extractSelector(args []string, offset int) string {
	if len(args) <= offset {
		return ""
	}
	return strings.Join(args[offset:], " ")
}

func sanitizeName(s string) string {
	s = strings.TrimSpace(s)
	r := strings.NewReplacer(
		"#", "", ".", "_", "[", "_", "]", "", "=", "_",
		" ", "_", "/", "_", ":", "_", "@", "", "'", "", `"`, "",
	)
	name := r.Replace(s)
	if len(name) > 30 {
		name = name[:30]
	}
	return name
}

func mergeMaps(a, b map[string]string) map[string]string {
	m := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		m[k] = v
	}
	for k, v := range b {
		m[k] = v
	}
	return m
}

func parseJSONMap(s string, v interface{}) error {
	return json.Unmarshal([]byte(s), v)
}

// isSession checks if the given arg is a session name (not a URL).
func (r *recorder) isSession(arg string) bool {
	return !strings.HasPrefix(arg, "http://") && !strings.HasPrefix(arg, "https://")
}
