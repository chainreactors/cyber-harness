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

func TestMediaHelpersPreserveDataAndURI(t *testing.T) {
	image := MediaData("image", "image/png", "shot.png", []byte("png"))
	if media := image.GetMedia(); media.GetKind() != "image" || media.GetResource().GetFilename() != "shot.png" || string(media.GetResource().GetData()) != "png" {
		t.Fatalf("image media = %+v", media)
	}
	video := MediaURI("video", "video/mp4", "capture.mp4", ".aiscan/record/capture.mp4")
	if media := video.GetMedia(); media.GetKind() != "video" || media.GetResource().GetMediaType() != "video/mp4" || media.GetResource().GetUri() != ".aiscan/record/capture.mp4" {
		t.Fatalf("video media = %+v", media)
	}
}
