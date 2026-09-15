package extension

import "testing"

func TestServicesTypedPublication(t *testing.T) {
	s := NewServices()
	key := ServiceOf[string]("name")
	if err := s.Provide(key, "cyber"); err != nil {
		t.Fatal(err)
	}
	if err := s.Provide(key, "duplicate"); err == nil {
		t.Fatal("duplicate service accepted")
	}
	s.Seal()
	value, err := Get[string](s, key)
	if err != nil || value != "cyber" {
		t.Fatalf("resolve: %q %v", value, err)
	}
	if err := s.Provide(ServiceOf[int]("count"), 1); err == nil {
		t.Fatal("sealed service accepted")
	}
}
