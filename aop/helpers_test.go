package aop

import (
	"bytes"
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestProviderFrameJSONAndBinaryRoundTrip(t *testing.T) {
	original := &Event{Payload: &Event_ProviderFrame{ProviderFrame: &ProviderFrame{
		Provider: "openai", Protocol: "responses", EventType: "response.output_text.delta",
		Direction: Direction_DIRECTION_RESPONSE, Transport: "sse",
		Payload:   []byte("event: response.output_text.delta\ndata: {\"delta\":\"hi\"}\n\n"),
		MediaType: "text/event-stream",
	}}}

	jsonData, err := protojson.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromJSON := new(Event)
	if err := protojson.Unmarshal(jsonData, fromJSON); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(original, fromJSON) {
		t.Fatalf("protojson round trip changed event")
	}

	binary, err := proto.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	fromBinary := new(Event)
	if err := proto.Unmarshal(binary, fromBinary); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fromJSON, fromBinary) {
		t.Fatalf("JSON and binary decoded messages differ")
	}
	if !bytes.Equal(original.GetProviderFrame().Payload, fromBinary.GetProviderFrame().Payload) {
		t.Fatalf("provider bytes changed")
	}
}
