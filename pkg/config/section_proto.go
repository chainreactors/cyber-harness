package config

import (
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/types/known/structpb"
)

func ValuesFromProto(values map[string]*structpb.Struct) Values {
	out := Values{}
	for key, value := range values {
		if value != nil {
			out[key] = value.AsMap()
		}
	}
	return out
}
func ValuesToProto(values Values) (map[string]*structpb.Struct, error) {
	out := map[string]*structpb.Struct{}
	for key, value := range values {
		v, err := structpb.NewStruct(value)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}
func (r *Sections) ProtoViews(values map[string]*structpb.Struct) map[string]*types.ExtensionView {
	plain, secrets := r.View(ValuesFromProto(values))
	out := map[string]*types.ExtensionView{}
	for key, value := range plain {
		v, err := structpb.NewStruct(value)
		if err == nil {
			out[key] = &types.ExtensionView{Values: v, ConfiguredSecrets: secrets[key]}
		}
	}
	return out
}
