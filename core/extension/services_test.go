package extension

import "testing"

func TestValidateServices(t *testing.T) {
	if err := ValidateServices(Descriptor{ID: "consumer", Requires: []Service{{ID: "tool"}}}, Descriptor{ID: "harness", Provides: []Service{{ID: "tool"}}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateServices(Descriptor{ID: "consumer", Requires: []Service{{ID: "missing"}}}); err == nil {
		t.Fatal("missing service accepted")
	}
	if err := ValidateServices(Descriptor{ID: "a", Provides: []Service{{ID: "tool"}}}, Descriptor{ID: "b", Provides: []Service{{ID: "tool"}}}); err == nil {
		t.Fatal("duplicate provider accepted")
	}
	if err := ValidateServices(Descriptor{ID: "consumer", Requires: []Service{{ID: "optional"}}, Optional: []Service{{ID: "optional"}}}); err != nil {
		t.Fatal(err)
	}
}
