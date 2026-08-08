package runner

import "reflect"

// isNilIOADependency handles Go's typed-nil interface case. IOA clients are
// commonly stored behind protocol interfaces; assigning a nil *Client to one
// of those interfaces makes the interface itself non-nil and a plain
// dependency == nil check is therefore insufficient.
func isNilIOADependency(dependency any) bool {
	if dependency == nil {
		return true
	}
	value := reflect.ValueOf(dependency)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
