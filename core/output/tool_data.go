package output

import "time"

type ToolDataEvent struct {
	Tool      string    `json:"tool"`
	Kind      string    `json:"kind"`
	Target    string    `json:"target,omitempty"`
	Data      any       `json:"data"`
	Timestamp time.Time `json:"timestamp"`
}

const (
	ToolDataService  = "service"
	ToolDataWeb      = "web"
	ToolDataWeakpass = "weakpass"
	ToolDataVuln     = "vuln"
)
