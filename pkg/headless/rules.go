//go:build browser

// Ported from nuclei pkg/protocols/headless/engine (rules.go + page_actions.go:164-169).

package headless

import "sync"

// rule stores a request/response modification rule.
// Matches nuclei's rule struct with sync.Once for SetMethod one-shot semantics.
type rule struct {
	*sync.Once
	Action ActionType
	Part   string // "request" or "response"
	Args   map[string]string
}

// newRule creates a rule. SetMethod gets a sync.Once so it only applies to the first request.
func newRule(action ActionType, part string, args map[string]string) rule {
	r := rule{
		Action: action,
		Part:   part,
		Args:   args,
	}
	if action == ActionSetMethod {
		r.Once = &sync.Once{}
	}
	return r
}

// Do wraps sync.Once.Do for SetMethod; other actions execute directly.
func (r *rule) Do(fn func()) {
	if r.Once != nil {
		r.Once.Do(fn)
	} else {
		fn()
	}
}

// containsModificationActionType checks if an action type is a request/response modifier.
func containsModificationActionType(at ActionType) bool {
	switch at {
	case ActionSetMethod, ActionAddHeader, ActionSetHeader, ActionDeleteHeader, ActionSetBody:
		return true
	}
	return false
}

// actionsContainModifications checks if any action in the list is a modifier.
func actionsContainModifications(actions []*Action) bool {
	for _, a := range actions {
		if containsModificationActionType(a.ActionType.ActionType) {
			return true
		}
	}
	return false
}
