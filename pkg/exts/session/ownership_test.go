package session_test

import (
	"reflect"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
)

func TestBusinessCapabilitiesCannotOwnExtensionLifetimes(t *testing.T) {
	for _, capability := range []reflect.Type{
		reflect.TypeFor[*sessionext.Session](),
		reflect.TypeFor[*sessionext.Runtime](),
	} {
		for _, method := range []string{"Load", "Close"} {
			if _, exists := capability.MethodByName(method); exists {
				t.Errorf("business capability %s exposes lifecycle method %s", capability, method)
			}
		}
	}
	if _, exists := reflect.TypeFor[*sessionext.Session]().MethodByName("Agent"); exists {
		t.Fatal("Session exposes its mutable internal Agent")
	}
	if _, exists := reflect.TypeFor[*sessionext.Extension]().MethodByName("Run"); exists {
		t.Fatal("Session Extension duplicates its Runtime execution API")
	}
	for _, method := range []string{"OpenSession", "EnsureSession", "Observe", "RunSession"} {
		if _, exists := reflect.TypeFor[*sessionext.Extension]().MethodByName(method); exists {
			t.Errorf("Session Extension promotes business method %s", method)
		}
	}
	owner := reflect.TypeFor[*sessionext.Extension]()
	if !owner.Implements(reflect.TypeFor[extension.Extension]()) {
		t.Errorf("%s is not managed by the common extension contract", owner)
	}
}
