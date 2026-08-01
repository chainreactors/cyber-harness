package aop

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type interopFixture struct {
	Event            json.RawMessage `json:"event"`
	BinaryBase64     string          `json:"binaryBase64"`
	ProviderPayloads struct {
		OpenAIBase64    string `json:"openaiBase64"`
		AnthropicBase64 string `json:"anthropicBase64"`
	} `json:"providerPayloads"`
}

func TestInteropFixtureMatchesProtoBinaryAndProtoJSON(t *testing.T) {
	path := filepath.Join("..", "web", "frontend", "cyber-ui", "packages", "aop", "fixtures", "interop.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture interopFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	event := new(Event)
	if err := protojson.Unmarshal(fixture.Event, event); err != nil {
		t.Fatal(err)
	}
	binary, err := proto.MarshalOptions{Deterministic: true}.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	got := base64.StdEncoding.EncodeToString(binary)
	if got != fixture.BinaryBase64 {
		t.Fatalf("binaryBase64 = %q", got)
	}
	openAI, err := base64.StdEncoding.DecodeString(fixture.ProviderPayloads.OpenAIBase64)
	if err != nil || string(openAI) != string(event.GetProviderFrame().Payload) {
		t.Fatalf("OpenAI payload mismatch: %q, %v", openAI, err)
	}
	if _, err := base64.StdEncoding.DecodeString(fixture.ProviderPayloads.AnthropicBase64); err != nil {
		t.Fatalf("Anthropic payload: %v", err)
	}
	jsonRoundTrip, err := protojson.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := new(Event)
	if err := protojson.Unmarshal(jsonRoundTrip, fromJSON); err != nil || !proto.Equal(event, fromJSON) {
		t.Fatalf("protobuf JSON round trip failed: %v", err)
	}
}
