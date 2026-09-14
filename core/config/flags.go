package config

// FlagGroup declares parser-bound options without starting a runtime. The host
// collects groups from its selected extensions before parsing arguments.
type FlagGroup struct {
	Name        string
	Description string
	Options     any
}
