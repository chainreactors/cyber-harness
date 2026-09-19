package client

import (
	"net/url"

	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/types/known/structpb"
)

// RedactView removes URL userinfo from the generic extension view. Token
// redaction and configured-secret reporting are owned by config.Sections.
func RedactView(view *types.ConfigView) {
	if view == nil {
		return
	}
	extension := view.GetExtensions()[ConfigKey]
	if extension == nil || extension.GetValues() == nil {
		return
	}
	field := extension.GetValues().GetFields()["url"]
	if field == nil {
		return
	}
	endpoint := field.GetStringValue()
	if parsed, err := url.Parse(endpoint); err == nil {
		parsed.User = nil
		endpoint = parsed.String()
	}
	extension.Values.Fields["url"] = structpb.NewStringValue(endpoint)
}
