//go:build tools

package resources

// Keep generator-only dependencies visible to go mod tidy on every platform.
import _ "sigs.k8s.io/yaml"
