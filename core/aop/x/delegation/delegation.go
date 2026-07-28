package delegation

import "github.com/chainreactors/aiscan/core/aop"

const NS = "delegation"

func Get(event aop.Event) (DelegationDetail, bool, error) {
	return aop.Ext[DelegationDetail](event, NS)
}

func Set(event *aop.Event, value DelegationDetail) error {
	return aop.SetExt(event, NS, value)
}
