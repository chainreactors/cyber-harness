//go:build full

package main

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/types"
	"os"
	"path/filepath"
	"testing"
)

func TestWebConfigExtensionSavePreservesOmittedSectionsAndSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aiscan.yaml")
	original := []byte("ioa:\n  url: http://ioa.test\n  token: secret\n  space: team\nextensions:\n  ioa.server:\n    token: server-secret\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	store := &webConfigStore{explicit: path}
	for _, incoming := range []*types.DistributeConfig{{}, {Ioa: &types.IOAConfig{Url: "http://ioa.test", Space: ""}}} {
		prepared, err := store.PrepareDistributeConfig(t.Context(), incoming)
		if err != nil {
			t.Fatal(err)
		}
		defer store.DiscardDistributeConfig(prepared)
		values := cfg.ValuesFromProto(prepared.Config.Extensions)
		if values["ioa.client"]["token"] != "secret" || values["ioa.server"]["token"] != "server-secret" {
			t.Fatalf("secrets lost: %#v", values)
		}
		if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
			t.Fatal(err)
		}
	}
	_, _, stored, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ValuesFromProto(stored.Extensions)["ioa.client"]["space"] != "" {
		t.Fatal("explicit empty space not saved")
	}
	before, _ := os.ReadFile(path)
	invalid, _ := cfg.ValuesToProto(cfg.Values{"ioa.client": {"url": "invalid"}})
	if prepared, err := store.PrepareDistributeConfig(t.Context(), &types.DistributeConfig{Extensions: invalid}); err == nil || prepared != nil {
		t.Fatal("invalid extension staged")
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("validation failure changed file")
	}
}
