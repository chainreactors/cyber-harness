package deps

import "testing"

func TestBagUsesKeyIdentity(t *testing.T) {
	first := NewKey[string]("shared")
	second := NewKey[string]("shared")
	bag := New()
	Set(bag, first, "value")

	if got, ok := Get(bag, first); !ok || got != "value" {
		t.Fatalf("Get(first) = %q, %v", got, ok)
	}
	if _, ok := Get(bag, second); ok {
		t.Fatal("distinct keys with the same name shared a value")
	}
}

func TestBagSupportsTypedValues(t *testing.T) {
	key := NewKey[int]("count")
	bag := New()
	Set(bag, key, 42)

	if got, ok := Get(bag, key); !ok || got != 42 {
		t.Fatalf("Get(count) = %d, %v", got, ok)
	}
	if !Has(bag, key) {
		t.Fatal("Has(count) = false")
	}
}

func TestBagNilOperationsAreSafe(t *testing.T) {
	key := NewKey[string]("optional")
	Set[string](nil, key, "ignored")
	if got, ok := Get[string](nil, key); ok || got != "" {
		t.Fatalf("Get(nil) = %q, %v", got, ok)
	}
}
