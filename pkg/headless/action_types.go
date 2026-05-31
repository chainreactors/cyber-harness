//go:build browser

package headless

import (
	"encoding/json"
	"strings"
)

// ActionType defines the type of browser action to perform.
// Values are compatible with nuclei's headless protocol.
type ActionType int8

const (
	ActionNavigate    ActionType = iota + 1 // navigate to a URL
	ActionScript                            // execute JavaScript
	ActionClick                             // left-click an element
	ActionRightClick                        // right-click an element
	ActionTextInput                         // type text into an input
	ActionScreenshot                        // capture screenshot
	ActionTimeInput                         // set a time input value
	ActionSelectInput                       // select an option
	ActionFilesInput                        // set file input
	ActionWaitDOM                           // wait for DOMContentLoaded
	ActionWaitFCP                           // wait for First Contentful Paint
	ActionWaitFMP                           // wait for First Meaningful Paint
	ActionWaitIdle                          // wait for network idle
	ActionWaitLoad                          // wait for page load
	ActionWaitStable                        // wait for page stability
	ActionGetResource                       // fetch a sub-resource
	ActionExtract                           // extract element content
	ActionSetMethod                         // override request method
	ActionAddHeader                         // append a request header
	ActionSetHeader                         // replace a request header
	ActionDeleteHeader                      // remove a request header
	ActionSetBody                           // override request body
	ActionWaitEvent                         // wait for a DOM/CDP event
	ActionKeyboard                          // press a key combination
	ActionDebug                             // log debug info
	ActionSleep                             // sleep for duration
	ActionWaitVisible                       // wait for element visibility
	ActionDialog                            // handle JS dialog (deprecated, use waitdialog)
	ActionWaitDialog                        // wait for JS dialog and capture type+message
)

var actionTypeNames = map[ActionType]string{
	ActionNavigate:    "navigate",
	ActionScript:      "script",
	ActionClick:       "click",
	ActionRightClick:  "rightclick",
	ActionTextInput:   "text",
	ActionScreenshot:  "screenshot",
	ActionTimeInput:   "time",
	ActionSelectInput: "select",
	ActionFilesInput:  "files",
	ActionWaitDOM:     "waitdom",
	ActionWaitFCP:     "waitfcp",
	ActionWaitFMP:     "waitfmp",
	ActionWaitIdle:    "waitidle",
	ActionWaitLoad:    "waitload",
	ActionWaitStable:  "waitstable",
	ActionGetResource: "getresource",
	ActionExtract:     "extract",
	ActionSetMethod:   "setmethod",
	ActionAddHeader:   "addheader",
	ActionSetHeader:   "setheader",
	ActionDeleteHeader: "deleteheader",
	ActionSetBody:     "setbody",
	ActionWaitEvent:   "waitevent",
	ActionKeyboard:    "keyboard",
	ActionDebug:       "debug",
	ActionSleep:       "sleep",
	ActionWaitVisible:  "waitvisible",
	ActionDialog:       "dialog",
	ActionWaitDialog:   "waitdialog",
}

var actionTypeMapping = func() map[string]ActionType {
	m := make(map[string]ActionType, len(actionTypeNames))
	for k, v := range actionTypeNames {
		m[v] = k
	}
	return m
}()

func (a ActionType) String() string {
	if s, ok := actionTypeNames[a]; ok {
		return s
	}
	return "unknown"
}

// ActionTypeHolder wraps ActionType for YAML/JSON marshaling.
type ActionTypeHolder struct {
	ActionType ActionType
}

func (h ActionTypeHolder) String() string {
	return h.ActionType.String()
}

func (h ActionTypeHolder) MarshalYAML() (interface{}, error) {
	return h.ActionType.String(), nil
}

func (h *ActionTypeHolder) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var raw interface{}
	if err := unmarshal(&raw); err != nil {
		return err
	}
	// YAML null (action: with no value) or empty string → script action.
	if raw == nil {
		h.ActionType = ActionScript
		return nil
	}
	s, ok := raw.(string)
	if !ok {
		return &json.UnsupportedValueError{}
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		h.ActionType = ActionScript
		return nil
	}
	if at, okMap := actionTypeMapping[s]; okMap {
		h.ActionType = at
		return nil
	}
	return &json.UnsupportedValueError{}
}

func (h ActionTypeHolder) MarshalJSON() ([]byte, error) {
	return json.Marshal(h.ActionType.String())
}

func (h *ActionTypeHolder) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if at, ok := actionTypeMapping[strings.ToLower(s)]; ok {
		h.ActionType = at
		return nil
	}
	return &json.UnsupportedValueError{}
}
