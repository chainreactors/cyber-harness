//go:build full

package engine

import "testing"

func TestMergeReconOptionsFofaFields(t *testing.T) {
	base := ReconOptions{FofaEmail: "old@example.com", FofaKey: "oldkey"}
	got := mergeReconOptions(base, ReconOptions{FofaEmail: "new@example.com", FofaKey: "newkey"})
	if got.FofaEmail != "new@example.com" || got.FofaKey != "newkey" {
		t.Fatalf("merge failed: %#v", got)
	}
}

func TestMergeReconOptionsEmptyDoesNotOverwrite(t *testing.T) {
	base := ReconOptions{FofaEmail: "keep@example.com", IngressProxy: "socks5://keep"}
	got := mergeReconOptions(base, ReconOptions{})
	if got.FofaEmail != "keep@example.com" || got.IngressProxy != "socks5://keep" {
		t.Fatalf("empty merge overwrote: %#v", got)
	}
}
