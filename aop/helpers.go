package aop

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const JSONMediaType = "application/json"
const ProtoJSONMediaType = "application/protobuf+json"

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

func ProtoJSONValue(value proto.Message) (*EncodedValue, error) {
	if value == nil {
		return nil, fmt.Errorf("protobuf value is required")
	}
	data, err := protojson.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &EncodedValue{Data: data, MediaType: ProtoJSONMediaType}, nil
}

func DecodeProtoJSON(value *EncodedValue, target proto.Message) error {
	if value == nil || target == nil {
		return fmt.Errorf("encoded value and target are required")
	}
	return protojson.Unmarshal(value.Data, target)
}

func SetProtoExtension(event *Event, namespace string, value proto.Message) error {
	if event == nil {
		return fmt.Errorf("event is required")
	}
	encoded, err := ProtoJSONValue(value)
	if err != nil {
		return err
	}
	for _, extension := range event.Extensions {
		if extension.Namespace == namespace {
			extension.Value = encoded
			return nil
		}
	}
	event.Extensions = append(event.Extensions, &Extension{Namespace: namespace, Value: encoded})
	return nil
}

func ProtoExtension(event *Event, namespace string, target proto.Message) (bool, error) {
	if event == nil {
		return false, nil
	}
	for _, extension := range event.Extensions {
		if extension.Namespace == namespace {
			return true, DecodeProtoJSON(extension.Value, target)
		}
	}
	return false, nil
}

func SetJSONExtension(event *Event, namespace string, value any) error {
	if event == nil {
		return fmt.Errorf("event is required")
	}
	encoded, err := JSONValue(value)
	if err != nil {
		return err
	}
	for _, extension := range event.Extensions {
		if extension.Namespace == namespace {
			extension.Value = encoded
			return nil
		}
	}
	event.Extensions = append(event.Extensions, &Extension{Namespace: namespace, Value: encoded})
	return nil
}

func GetJSONExtension[T any](event *Event, namespace string) (T, bool, error) {
	var zero T
	if event == nil {
		return zero, false, nil
	}
	for _, extension := range event.Extensions {
		if extension.Namespace == namespace {
			value, err := DecodeJSON[T](extension.Value)
			return value, true, err
		}
	}
	return zero, false, nil
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
		return event.GetExtension().GetType()
	case *Event_ProviderFrame:
		return "provider.frame"
	default:
		return ""
	}
}
