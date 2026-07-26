//go:build !full

package engine

type UncoverEngine struct{}

func (e *UncoverEngine) Sources() []string { return nil }
func (e *UncoverEngine) Close() error      { return nil }
