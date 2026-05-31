//go:build browser

// Request implements neutron's protocols.Request for the headless protocol.
// Ported from nuclei pkg/protocols/headless/request.go.

package headless

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/chainreactors/neutron/common"
	"github.com/chainreactors/neutron/operators"
	"github.com/chainreactors/neutron/protocols"
	"github.com/go-rod/rod/lib/proto"
)

var _ protocols.Request = &Request{}

// Request defines a headless browser action sequence with matchers/extractors.
type Request struct {
	operators.Operators `yaml:",inline" json:",inline"`

	ID               string                 `yaml:"id,omitempty" json:"id,omitempty"`
	Steps            []*Action              `yaml:"steps" json:"steps"`
	Payloads         map[string]interface{} `yaml:"payloads,omitempty" json:"payloads,omitempty"`
	AttackType       string                 `yaml:"attack,omitempty" json:"attack,omitempty"`
	UserAgent        string                 `yaml:"user-agent,omitempty" json:"user-agent,omitempty"`
	StopAtFirstMatch bool                   `yaml:"stop-at-first-match,omitempty" json:"stop-at-first-match,omitempty"`
	SelfContained    bool                   `yaml:"self-contained,omitempty" json:"self-contained,omitempty"`
	DisableCookie    bool                   `yaml:"disable-cookie,omitempty" json:"disable-cookie,omitempty"`

	engine            *Engine
	generator         *protocols.Generator
	CompiledOperators *operators.Operators `yaml:"-" json:"-"`
	options           *protocols.ExecuterOptions
}

func (r *Request) Type() protocols.ProtocolType {
	return HeadlessProtocol
}

// Compile prepares the request for execution.
// Creates payload generator if payloads are defined.
func (r *Request) Compile(options *protocols.ExecuterOptions) error {
	r.options = options

	// Create payload generator if payloads defined.
	if len(r.Payloads) > 0 {
		attackType := protocols.Sniper
		if at, ok := protocols.StringToType[r.AttackType]; ok {
			attackType = at
		}
		generator, err := protocols.NewGenerator(r.Payloads, attackType)
		if err != nil {
			return fmt.Errorf("could not create payload generator: %w", err)
		}
		r.generator = generator
	}

	if len(r.Matchers) > 0 || len(r.Extractors) > 0 {
		compiled := &r.Operators
		if err := compiled.Compile(); err != nil {
			return fmt.Errorf("could not compile operators: %w", err)
		}
		r.CompiledOperators = compiled
	}
	return nil
}

func (r *Request) Requests() int {
	return 1
}

func (r *Request) GetCompiledOperators() []*operators.Operators {
	return []*operators.Operators{r.CompiledOperators}
}

func (r *Request) SetEngine(e *Engine) {
	r.engine = e
}

// ExecuteWithResults runs the headless actions with payload iteration.
func (r *Request) ExecuteWithResults(input *protocols.ScanContext, dynamicValues, previous map[string]interface{}, callback protocols.OutputEventCallback) error {
	if r.engine == nil {
		return fmt.Errorf("headless engine not initialized")
	}

	// Self-contained mode: extract URL from first navigate action.
	target := input.Input
	if r.SelfContained {
		if u := extractBaseURLFromActions(r.Steps); u != "" {
			target = u
		}
	}

	// Build base variables.
	vars := make(map[string]interface{})
	for k, v := range previous {
		vars[k] = v
	}
	for k, v := range dynamicValues {
		vars[k] = v
	}
	if input.Payloads != nil {
		for k, v := range input.Payloads {
			vars[k] = v
		}
	}

	// Evaluate template-level variables using neutron's Variable.Evaluate.
	if r.options != nil && r.options.Variables.Len() > 0 {
		variablesMap := r.options.Variables.Evaluate(vars)
		vars = common.MergeMaps(vars, variablesMap)
	}

	// Add CLI-provided payload options.
	if r.options != nil && r.options.Options != nil {
		optionVars := protocols.BuildPayloadFromOptions(r.options.Options)
		vars = common.MergeMaps(vars, optionVars)
	}

	vars["BaseURL"] = target
	vars["Hostname"] = target

	// Parse hostname for additional variables.
	if parsed, err := url.Parse(target); err == nil {
		vars["Host"] = parsed.Host
		vars["RootURL"] = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
		vars["Scheme"] = parsed.Scheme
		vars["Path"] = parsed.Path
	}

	var gotMatches bool

	// Payload iteration (sniper/pitchfork/clusterbomb).
	if r.generator != nil {
		iterator := r.generator.NewIterator()
		for {
			value, ok := iterator.Value()
			if !ok {
				break
			}
			if gotMatches && r.StopAtFirstMatch {
				return nil
			}
			mergedVars := common.MergeMaps(vars, value)
			matched, err := r.executeRequestWithPayloads(target, mergedVars, previous, callback)
			if err != nil {
				return err
			}
			if matched {
				gotMatches = true
			}
		}
	} else {
		_, err := r.executeRequestWithPayloads(target, vars, previous, callback)
		if err != nil {
			return err
		}
	}
	return nil
}

// executeRequestWithPayloads executes the action sequence with a specific set of variables.
func (r *Request) executeRequestWithPayloads(target string, vars, previous map[string]interface{}, callback protocols.OutputEventCallback) (bool, error) {
	// Create isolated instance for this execution.
	instance, err := r.engine.NewInstance()
	if err != nil {
		// Fallback to non-isolated page if incognito fails.
		return r.executeOnPage(target, vars, previous, callback)
	}
	defer instance.Close()

	// Create page in incognito context.
	page, err := instance.browser.Page(proto.TargetCreateTarget{URL: ""})
	if err != nil {
		return false, fmt.Errorf("could not create page: %w", err)
	}
	defer page.Close()

	executor := NewPageWithInstance(page, instance, vars)
	defer executor.Close()

	// Set input URL for parameter merging.
	if parsed, err := url.Parse(target); err == nil {
		executor.SetInputURL(parsed)
	}

	actionData, err := executor.ExecuteActions(r.Steps)
	if err != nil {
		return false, fmt.Errorf("action execution failed: %w", err)
	}

	return r.processResults(executor, actionData, target, previous, callback)
}

// executeOnPage is a fallback that creates a page directly on the engine browser.
func (r *Request) executeOnPage(target string, vars, previous map[string]interface{}, callback protocols.OutputEventCallback) (bool, error) {
	page, err := r.engine.NewPage()
	if err != nil {
		return false, fmt.Errorf("could not create page: %w", err)
	}
	defer page.Close()

	executor := NewPage(page, r.engine, vars)
	defer executor.Close()

	if parsed, err := url.Parse(target); err == nil {
		executor.SetInputURL(parsed)
	}

	actionData, err := executor.ExecuteActions(r.Steps)
	if err != nil {
		return false, fmt.Errorf("action execution failed: %w", err)
	}

	return r.processResults(executor, actionData, target, previous, callback)
}

// processResults builds DSL map, runs operators, and invokes callback.
func (r *Request) processResults(executor *Page, actionData ActionData, target string, previous map[string]interface{}, callback protocols.OutputEventCallback) (bool, error) {
	resp := executor.GetResponseData()
	internalEvent := r.responseToDSLMap(resp, actionData, target)
	for k, v := range previous {
		internalEvent[k] = v
	}

	var event *protocols.InternalWrappedEvent
	var matched bool
	if r.CompiledOperators != nil {
		result, _ := r.CompiledOperators.Execute(internalEvent, r.Match, r.Extract)
		event = protocols.CreateEventWithOperatorResults(r, internalEvent, result)
		if result != nil {
			matched = result.Matched
		}
	} else {
		event = &protocols.InternalWrappedEvent{InternalEvent: internalEvent}
	}

	callback(event)
	return matched, nil
}

func (r *Request) Match(data map[string]interface{}, matcher *operators.Matcher) (bool, []string) {
	itemStr := r.getMatchPart(matcher.Part, data)

	switch matcher.GetType() {
	case operators.StatusMatcher:
		statusCode := 0
		if sc, ok := data["status_code"]; ok {
			switch v := sc.(type) {
			case int:
				statusCode = v
			case float64:
				statusCode = int(v)
			}
		}
		return matcher.Result(matcher.MatchStatusCode(statusCode)), []string{}
	case operators.SizeMatcher:
		return matcher.Result(matcher.MatchSize(len(itemStr))), []string{}
	case operators.WordsMatcher:
		return matcher.ResultWithMatchedSnippet(matcher.MatchWords(itemStr, data))
	case operators.RegexMatcher:
		return matcher.ResultWithMatchedSnippet(matcher.MatchRegex(itemStr))
	case operators.BinaryMatcher:
		return matcher.ResultWithMatchedSnippet(matcher.MatchBinary(itemStr))
	case operators.DSLMatcher:
		return matcher.Result(matcher.MatchDSL(data)), []string{}
	}
	return false, []string{}
}

func (r *Request) Extract(data map[string]interface{}, extractor *operators.Extractor) map[string]struct{} {
	itemStr := r.getMatchPart(extractor.Part, data)

	switch extractor.GetType() {
	case operators.RegexExtractor:
		return extractor.ExtractRegex(itemStr)
	case operators.KValExtractor:
		return extractor.ExtractKval(data)
	case operators.DSLExtractor:
		return extractor.ExtractDSL(data)
	}
	return nil
}

func (r *Request) MakeResultEventItem(wrapped *protocols.InternalWrappedEvent) *protocols.ResultEvent {
	return &protocols.ResultEvent{
		TemplateID:       common.ToString(wrapped.InternalEvent["template-id"]),
		Type:             common.ToString(wrapped.InternalEvent["type"]),
		Host:             common.ToString(wrapped.InternalEvent["host"]),
		Matched:          common.ToString(wrapped.InternalEvent["matched"]),
		ExtractedResults: wrapped.OperatorsResult.OutputExtracts,
		Timestamp:        time.Now(),
	}
}

func (r *Request) MakeResultEvent(wrapped *protocols.InternalWrappedEvent) []*protocols.ResultEvent {
	if wrapped.OperatorsResult == nil {
		return nil
	}
	return protocols.MakeDefaultResultEvent(r, wrapped)
}

func (r *Request) responseToDSLMap(resp *ResponseData, actionData ActionData, inputURL string) protocols.InternalEvent {
	event := protocols.InternalEvent{
		"type":        "headless",
		"host":        inputURL,
		"matched":     resp.URL,
		"url":         resp.URL,
		"status_code": resp.StatusCode,
		"data":        resp.Body,
		"body":        resp.Body,
		"resp":        resp.Body,
		"all":         resp.Body,
		"raw":         resp.Body,
	}

	var headerBuf strings.Builder
	for k, v := range resp.Headers {
		headerBuf.WriteString(k)
		headerBuf.WriteString(": ")
		headerBuf.WriteString(v)
		headerBuf.WriteString("\n")
	}
	event["header"] = headerBuf.String()

	var histBuf strings.Builder
	for _, h := range resp.History {
		histBuf.WriteString(h.RawRequest)
		histBuf.WriteString("\n---\n")
		histBuf.WriteString(h.RawResponse)
		histBuf.WriteString("\n===\n")
	}
	event["history"] = histBuf.String()

	for k, v := range actionData {
		event[k] = v
	}

	return event
}

func (r *Request) getMatchPart(part string, data protocols.InternalEvent) string {
	switch part {
	case "body", "all", "data", "resp", "":
		part = "raw"
	}
	if item, ok := data[part]; ok {
		return common.ToString(item)
	}
	return ""
}

// extractBaseURLFromActions extracts the URL from the first navigate action.
func extractBaseURLFromActions(steps []*Action) string {
	for _, step := range steps {
		if step.ActionType.ActionType == ActionNavigate {
			if u := step.GetArg("url"); u != "" {
				return u
			}
		}
	}
	return ""
}
