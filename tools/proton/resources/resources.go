// Package resources owns only proton rules; it does not load scanner engines.
package resources

//go:generate go run ../../resources/templates_gen.go -t ../../templates -o template.go -need found_keys,found_spray,found_filter_ext,found_filter_dir

func Config(name string) []byte { return loadEmbeddedConfig(name) }
