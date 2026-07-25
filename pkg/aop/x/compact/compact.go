package compact

import "github.com/chainreactors/aiscan/pkg/aop"

const (
	NS = "compact"

	StateStart = "compact_start"
	StateEnd   = "compact_end"
	StateError = "compact_error"
)

func GetDetail(event aop.Event) (Detail, bool, error) { return aop.Ext[Detail](event, NS) }
func SetDetail(event *aop.Event, value Detail) error  { return aop.SetExt(event, NS, value) }
