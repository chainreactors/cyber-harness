package session_test

import (
	"reflect"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	agentext "github.com/chainreactors/aiscan/pkg/exts/agent"
	"github.com/chainreactors/aiscan/pkg/exts/session"
)

func TestBorrowedCapabilitiesCannotOwnExtensionLifetimes(t *testing.T) {
	for _, capability := range []reflect.Type{
		reflect.TypeFor[*session.Manager](),
		reflect.TypeFor[*session.Session](),
		reflect.TypeFor[*agentext.Runtime](),
	} {
		for _, method := range []string{"Load", "Close"} {
			if _, exists := capability.MethodByName(method); exists {
				t.Errorf("borrowed %s exposes lifecycle method %s", capability, method)
			}
		}
	}
	if _, exists := reflect.TypeFor[*session.Session]().MethodByName("Agent"); exists {
		t.Fatal("Session exposes its mutable internal Agent")
	}
	if _, exists := reflect.TypeFor[*agentext.Extension]().MethodByName("Run"); exists {
		t.Fatal("Agent Extension duplicates its borrowed Runtime execution API")
	}
	for _, owner := range []reflect.Type{
		reflect.TypeFor[*session.Resource](),
		reflect.TypeFor[*agentext.Extension](),
	} {
		if !owner.Implements(reflect.TypeFor[extension.Extension]()) {
			t.Errorf("%s is not managed by the common extension contract", owner)
		}
	}
}
