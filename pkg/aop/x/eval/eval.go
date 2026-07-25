package eval

import "github.com/chainreactors/aiscan/pkg/aop"

const (
	NS = "eval"

	StateStart = "eval_start"
	StateEnd   = "eval_end"
	StateError = "eval_error"
)

func Get(event aop.Event) (Control, bool, error)      { return aop.Ext[Control](event, NS) }
func Set(event *aop.Event, value Control) error       { return aop.SetExt(event, NS, value) }
func GetDetail(event aop.Event) (Detail, bool, error) { return aop.Ext[Detail](event, NS) }
func SetDetail(event *aop.Event, value Detail) error  { return aop.SetExt(event, NS, value) }
