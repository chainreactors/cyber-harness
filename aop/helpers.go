package aop

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const JSONMediaType = "application/json"

func JSONValue(value any) (*EncodedValue, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &EncodedValue{Data: data, MediaType: JSONMediaType}, nil
}

func DecodeJSON[T any](value *EncodedValue) (T, error) {
	var decoded T
	if value == nil {
		return decoded, fmt.Errorf("encoded value is required")
	}
	if err := json.Unmarshal(value.Data, &decoded); err != nil {
		return decoded, err
	}
	return decoded, nil
}

// SetTypedExtension packs value into Event.extensions and replaces an existing
// extension with the same protobuf full name.
func SetTypedExtension(event *Event, value proto.Message) error {
	if event == nil {
		return fmt.Errorf("event is required")
	}
	encoded, err := anypb.New(value)
	if err != nil {
		return err
	}
	for _, extension := range event.Extensions {
		if extension != nil && extension.MessageName() == encoded.MessageName() {
			extension.TypeUrl = encoded.TypeUrl
			extension.Value = encoded.Value
			return nil
		}
	}
	event.Extensions = append(event.Extensions, encoded)
	return nil
}

// FindTypedExtension unmarshals the extension matching target's protobuf full
// name. Unknown extensions are ignored and remain preserved on the Event.
func FindTypedExtension(event *Event, target proto.Message) (bool, error) {
	if target == nil {
		return false, fmt.Errorf("extension target is required")
	}
	if event == nil {
		return false, nil
	}
	for _, extension := range event.Extensions {
		if extension != nil && extension.MessageIs(target) {
			return true, extension.UnmarshalTo(target)
		}
	}
	return false, nil
}

func Text(text string) *Content {
	return &Content{Value: &Content_Text{Text: &TextContent{Text: text}}}
}

func Reasoning(text string) *Content {
	return &Content{Value: &Content_Reasoning{Reasoning: &ReasoningContent{Text: text}}}
}

func Image(mediaType string, data []byte) *Content {
	return &Content{Value: &Content_Media{Media: &MediaContent{
		Kind: "image",
		Resource: &Resource{
			Source:    &Resource_Data{Data: data},
			MediaType: mediaType,
		},
	}}}
}

func Kind(event *Event) string {
	if event == nil {
		return ""
	}
	switch event.Payload.(type) {
	case *Event_SessionStarted:
		return "session.started"
	case *Event_SessionEnded:
		return "session.ended"
	case *Event_TurnStarted:
		return "turn.started"
	case *Event_TurnEnded:
		return "turn.ended"
	case *Event_Message:
		return "message"
	case *Event_MessageDelta:
		return "message.delta"
	case *Event_ToolCall:
		return "tool.call"
	case *Event_ToolCallDelta:
		return "tool.call.delta"
	case *Event_ToolResult:
		return "tool.result"
	case *Event_Usage:
		return "usage"
	case *Event_Error:
		return "error"
	case *Event_Status:
		return "status"
	case *Event_Extension:
		if extension := event.GetExtension(); extension != nil {
			return string(extension.MessageName())
		}
		return "extension"
	case *Event_ProviderFrame:
		return "provider.frame"
	default:
		return ""
	}
}
