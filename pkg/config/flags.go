package config

// FlagGroup declares parser-bound options without starting a runtime. The host
// collects supported option schemas before parsing; resolved configuration then
// selects the runtime extensions. No Scope or loaded Extension is required.
type FlagGroup struct {
	Name        string
	Description string
	Options     any
	// Hidden keeps the flags parseable but out of help output.
	Hidden bool
}
