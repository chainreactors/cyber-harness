package tool

// Definition describes a tool the LLM can invoke.
type Definition struct {
	Type     string  `json:"type"`
	Function FuncDef `json:"function"`
}

// FuncDef is the schema half of a Definition.
type FuncDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}
