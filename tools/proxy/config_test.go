package proxy

import (
	"context"
	"errors"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"os"
	"path/filepath"
	"testing"
)

func TestHubConstructionIsInert(t *testing.T) {
	workDir := t.TempDir()
	resource, err := NewHub(workDir, "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := resource.ProxyHub
	if hub.ProxyURL() != "" {
		t.Fatal("proxy listener started during construction")
	}
	if _, err := os.Stat(filepath.Join(workDir, ".cyber")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("construction touched the filesystem: %v", err)
	}
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if hub.ProxyURL() == "" {
		t.Fatal("Start did not open the proxy listener")
	}
	if err := resource.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := resource.Start(t.Context()); err == nil {
		t.Fatal("closed proxy infrastructure started again")
	}
}

func TestHubRejectsInvalidStorageBeforeConstruction(t *testing.T) {
	_, err := NewHub(t.TempDir(), "", true, nil, cfg.TrafficOptions{BodyStorage: "invalid"})
	if err == nil {
		t.Fatal("invalid body storage was accepted")
	}
}
