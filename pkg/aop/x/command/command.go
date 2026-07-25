package command

import "github.com/chainreactors/aiscan/pkg/aop"

const NS = "command"

type Detail struct {
	Line         string `json:"line"`
	Presentation string `json:"presentation,omitempty"`
}

func GetDetail(event aop.Event) (Detail, bool, error) { return aop.Ext[Detail](event, NS) }
func SetDetail(event *aop.Event, value Detail) error  { return aop.SetExt(event, NS, value) }
