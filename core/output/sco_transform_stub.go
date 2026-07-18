//go:build !cstx_native

package output

import "encoding/json"

func CSTXTransform(_ string, _ any) ([]json.RawMessage, error) { return nil, nil }
