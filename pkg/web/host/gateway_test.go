package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/proto"
)

func TestGatewayConfigCenterPreservesSecretsAndWritesFlatYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cyber.yaml")
	if err := os.WriteFile(path, []byte("gateway:\n  fofa:\n    key: account-secret\n  office:\n    kind: clash\n    url: https://example.test/token-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	codec := SharedConfigCodec()
	store := &FileConfigStore{Explicit: path, Codec: codec}
	_, _, current, err := store.GetDistributeConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	plain, metadata := codec.Sections.View(cfg.ValuesFromProto(current.Extensions))
	if strings.Contains(fmt.Sprint(plain), "secret") || len(metadata["gateway"]) != 2 {
		t.Fatalf("configuration center exposed credentials: %v %v", plain, metadata)
	}
	if _, exists := plain["gateway"]["fofa"]; !exists {
		t.Fatal("redaction lost instance identity")
	}
	next := proto.CloneOf(current)
	values := cfg.ValuesFromProto(next.Extensions)
	values["gateway"] = map[string]any{"fofa": map[string]any{"limit": 10}}
	next.Extensions, err = cfg.ValuesToProto(values)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := store.PrepareDistributeConfig(t.Context(), next)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DiscardDistributeConfig(prepared) })
	services := cfg.ValuesFromProto(prepared.Config.Extensions)["gateway"]
	if services["fofa"].(map[string]any)["key"] != "account-secret" {
		t.Fatal("partial edit lost the opaque account credential")
	}
	if _, exists := services["office"]; exists {
		t.Fatal("removed subscription was restored by secret preservation")
	}
	if err := store.CommitDistributeConfig(t.Context(), prepared); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "extensions:") || strings.Contains(string(data), "office:") || !strings.Contains(string(data), "gateway:") {
		t.Fatalf("unexpected persisted configuration: %s", data)
	}
	_, _, reloaded, err := store.GetDistributeConfig(t.Context())
	if err != nil || !proto.Equal(prepared.Config, reloaded) {
		t.Fatalf("flat configuration did not round trip: %v", err)
	}
}
