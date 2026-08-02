package aop_test

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type interopFixture struct {
	Envelope         json.RawMessage `json:"envelope"`
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
	envelope := new(aop.Envelope)
	if err := protojson.Unmarshal(fixture.Envelope, envelope); err != nil {
		t.Fatal(err)
	}
	if got := envelope.GetPayload().GetTypeUrl(); got != "type.googleapis.com/aop.ProtocolMessage" {
		t.Fatalf("payload type URL = %q", got)
	}
	core := new(aop.ProtocolMessage)
	if err := envelope.GetPayload().UnmarshalTo(core); err != nil {
		t.Fatal(err)
	}
	event := core.GetEvent()
	if event == nil {
		t.Fatal("fixture payload does not contain an event")
	}
	if len(event.Extensions) != 1 {
		t.Fatalf("event extensions = %d", len(event.Extensions))
	}
	progress := new(toolpb.Progress)
	if err := event.Extensions[0].UnmarshalTo(progress); err != nil {
		t.Fatal(err)
	}
	if progress.Tool != "fixture-tool" || progress.Text != "fixture progress" {
		t.Fatalf("typed extension = %#v", progress)
	}
	binary, err := proto.MarshalOptions{Deterministic: true}.Marshal(envelope)
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
	jsonRoundTrip, err := protojson.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := new(aop.Envelope)
	if err := protojson.Unmarshal(jsonRoundTrip, fromJSON); err != nil || !proto.Equal(envelope, fromJSON) {
		t.Fatalf("protobuf JSON round trip failed: %v", err)
	}
}

func TestSessionNodeURIPreservesFieldThreeBinaryCompatibility(t *testing.T) {
	// The node identity remains field 3, so persisted sessions decode without
	// rewriting even though the public field name is now node_uri.
	legacy := []byte{0x1a, 0x0c, 'n', 'o', 'd', 'e', ':', '/', '/', 'l', 'o', 'c', 'a', 'l'}
	session := new(aop.Session)
	if err := proto.Unmarshal(legacy, session); err != nil {
		t.Fatal(err)
	}
	if session.GetNodeUri() != "node://local" {
		t.Fatalf("node_uri = %q", session.GetNodeUri())
	}
}
