//go:build full

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
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
	return recordCommandResult(sess, cmd, args, "")
}

func recordCommandResult(sess *Session, cmd string, args []string, result string) bool {
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
			Action: headless.ActionDblClick,
			Args:   selectorArgs(sel),
		}

	case "fill":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionTextInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value, "clear": "true"}),
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
			Args:   mergeMaps(selectorArgs(args[1]), map[string]string{"keys": keys}),
		}

	case "select-option", "select":
		if len(args) < 3 {
			return false
		}
		sel := args[1]
		value := strings.Join(args[2:], " ")
		ra = RecordedAction{
			Action: headless.ActionSelectInput,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"value": value, "selected": "true"}),
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
			} else if args[i] == "--selector" && i+1 < len(args) {
				i++
				ra.Args = mergeMaps(ra.Args, selectorArgs(args[i]))
			}
		}

	case "set-input-files", "upload":
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
			Action: headless.ActionHover,
			Args:   selectorArgs(sel),
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
				Args:   selectorArgs(target),
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
			Action: headless.ActionReload,
			Args:   map[string]string{},
		}

	case "go-back", "back":
		ra = RecordedAction{
			Action: headless.ActionGoBack,
			Args:   map[string]string{},
		}

	case "go-forward", "forward":
		ra = RecordedAction{
			Action: headless.ActionGoForward,
			Args:   map[string]string{},
		}

	case "check":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionCheck,
			Args:   selectorArgs(sel),
		}

	case "uncheck":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionUncheck,
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
			Action: headless.ActionDispatchEvent,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"event": eventType}),
		}

	case "dialog":
		if len(args) >= 2 && args[1] == "--arm" {
			ra = RecordedAction{
				Action: headless.ActionDialog,
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

	case "content", "inner-html", "html":
		sel := "html"
		if len(args) >= 2 {
			sel = strings.Join(args[1:], " ")
		}
		ra = RecordedAction{
			Action: headless.ActionExtract,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"target": "html"}),
			Name:   sanitizeName(sel + "_html"),
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
				"target": "value",
			}),
			Name: sanitizeName(sel + "_value"),
		}

	case "set-viewport":
		if len(args) < 3 {
			return false
		}
		ra = RecordedAction{
			Action: headless.ActionSetViewport,
			Args:   map[string]string{"width": args[1], "height": args[2]},
		}

	case "focus", "blur":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		action := headless.ActionFocus
		if cmd == "blur" {
			action = headless.ActionBlur
		}
		ra = RecordedAction{Action: action, Args: selectorArgs(sel)}

	case "wait-for-url", "wait-for-request", "wait-for-response":
		if len(args) < 2 {
			return false
		}
		action := headless.ActionWaitURL
		if cmd == "wait-for-request" {
			action = headless.ActionWaitRequest
		} else if cmd == "wait-for-response" {
			action = headless.ActionWaitResponse
		}
		ra = RecordedAction{Action: action, Args: map[string]string{"url": strings.Join(args[1:], " ")}}

	case "set-content":
		if len(args) < 2 {
			return false
		}
		ra = RecordedAction{Action: headless.ActionSetContent, Args: map[string]string{"html": strings.Join(args[1:], " ")}}

	case "url", "title":
		ra = RecordedAction{
			Action: headless.ActionExtract,
			Args:   map[string]string{"target": cmd},
			Name:   cmd,
		}

	case "is-visible", "is-hidden", "is-checked", "is-disabled", "is-enabled":
		sel := extractSelector(args, 1)
		if sel == "" {
			return false
		}
		assertionType := strings.TrimPrefix(cmd, "is-")
		if strings.HasSuffix(strings.TrimSpace(result), "= false") {
			assertionType = map[string]string{
				"visible": "hidden", "hidden": "visible",
				"checked": "unchecked", "disabled": "enabled", "enabled": "disabled",
			}[assertionType]
		}
		ra = RecordedAction{
			Action: headless.ActionAssert,
			Args:   mergeMaps(selectorArgs(sel), map[string]string{"type": assertionType}),
		}

	case "localstorage-set", "sessionstorage-set":
		if len(args) < 3 {
			return false
		}
		storageType := strings.TrimSuffix(cmd, "-set")
		ra = RecordedAction{Action: headless.ActionStorage, Args: map[string]string{
			"storage": storageType, "operation": "set", "key": args[1], "value": strings.Join(args[2:], " "),
		}}

	case "localstorage-delete", "sessionstorage-delete":
		if len(args) < 2 {
			return false
		}
		storageType := strings.TrimSuffix(cmd, "-delete")
		ra = RecordedAction{Action: headless.ActionStorage, Args: map[string]string{
			"storage": storageType, "operation": "delete", "key": args[1],
		}}

	case "localstorage-clear", "sessionstorage-clear":
		storageType := strings.TrimSuffix(cmd, "-clear")
		ra = RecordedAction{Action: headless.ActionStorage, Args: map[string]string{
			"storage": storageType, "operation": "clear",
		}}

	case "localstorage-get", "sessionstorage-get":
		if len(args) < 2 {
			return false
		}
		storageType := strings.TrimSuffix(cmd, "-get")
		ra = RecordedAction{Action: headless.ActionExtract, Args: map[string]string{
			"target": "storage", "storage": storageType, "key": args[1],
		}, Name: sanitizeName(storageType + "_" + args[1])}

	case "localstorage-list", "sessionstorage-list":
		storageType := strings.TrimSuffix(cmd, "-list")
		ra = RecordedAction{Action: headless.ActionExtract, Args: map[string]string{
			"target": "storage", "storage": storageType,
		}, Name: sanitizeName(storageType)}

	case "cookie-set":
		if len(args) < 2 {
			return false
		}
		recorded := false
		for _, pair := range args[1:] {
			name, value, ok := strings.Cut(pair, "=")
			if !ok || name == "" {
				continue
			}
			sess.rec.record(RecordedAction{Action: headless.ActionCookie, Args: map[string]string{
				"operation": "set", "name": name, "value": value,
			}})
			recorded = true
		}
		return recorded

	case "cookie-delete":
		if len(args) < 2 {
			return false
		}
		ra = RecordedAction{Action: headless.ActionCookie, Args: map[string]string{
			"operation": "delete", "name": args[1],
		}}

	case "cookie-clear":
		ra = RecordedAction{Action: headless.ActionCookie, Args: map[string]string{"operation": "clear"}}

	case "cookie-get":
		if len(args) < 2 {
			return false
		}
		ra = RecordedAction{Action: headless.ActionExtract, Args: map[string]string{
			"target": "cookie", "name": args[1],
		}, Name: sanitizeName("cookie_" + args[1])}

	case "cookie-list":
		ra = RecordedAction{Action: headless.ActionExtract, Args: map[string]string{
			"target": "cookie",
		}, Name: "cookies"}

	case "dialog-accept", "dialog-dismiss":
		argsMap := map[string]string{"accept": strconv.FormatBool(cmd == "dialog-accept")}
		if cmd == "dialog-accept" && len(args) >= 2 {
			argsMap["prompt"] = strings.Join(args[1:], " ")
		}
		ra = RecordedAction{Action: headless.ActionDialog, Args: argsMap}

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
	return headless.ParseSelector(sel)
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
