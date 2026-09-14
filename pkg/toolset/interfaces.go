package toolset

import "github.com/chainreactors/aiscan/core/tool"

// Registrar contributes fixed tools before publication. It grants no execution
// or lifecycle authority.
type Registrar interface { Register(string, ...tool.Tool) error }
type Runtime interface { Registrar; tool.Executor }
