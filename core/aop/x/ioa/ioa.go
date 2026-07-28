package ioa

import "github.com/chainreactors/aiscan/core/aop"

const NS = "ioa"

func GetDetail(event aop.Event) (HandoffDetail, bool, error) {
	return aop.Ext[HandoffDetail](event, NS)
}
func SetDetail(event *aop.Event, value HandoffDetail) error { return aop.SetExt(event, NS, value) }
