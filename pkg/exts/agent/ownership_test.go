package agent_test

import (
	"reflect"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	agentext "github.com/chainreactors/cyber/pkg/exts/agent"
)

func TestBusinessCapabilitiesCannotOwnExtensionLifetimes(t *testing.T) {
	for _, capability := range []reflect.Type{
		reflect.TypeFor[*agentext.Loop](),
	} {
		for _, method := range []string{"Load", "Close"} {
			if _, exists := capability.MethodByName(method); exists {
				t.Errorf("business capability %s exposes lifecycle method %s", capability, method)
			}
		}
	}
	if _, exists := reflect.TypeFor[*agentext.Extension]().MethodByName("Run"); exists {
		t.Fatal("Agent Extension duplicates its Loop execution API")
	}
	for _, method := range []string{"OpenSession", "EnsureSession", "Observe", "RunSession"} {
		if _, exists := reflect.TypeFor[*agentext.Extension]().MethodByName(method); exists {
			t.Errorf("Agent Extension promotes business method %s", method)
		}
	}
	owner := reflect.TypeFor[*agentext.Extension]()
	if !owner.Implements(reflect.TypeFor[extension.Extension]()) {
		t.Errorf("%s is not managed by the common extension contract", owner)
	}
}
