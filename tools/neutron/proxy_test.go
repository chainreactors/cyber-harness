package neutron_test

import (
	"fmt"
	"testing"

	"github.com/chainreactors/cyber/tools/neutron"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
)

func TestNeutronProxyIsInstanceLocal(t *testing.T) {
	original := fmt.Sprintf("%p/%p", neutronhttp.DefaultOption.Proxy, neutronhttp.DefaultTransport.Proxy)
	first := neutron.New(nil, nil).WithProxy("socks5://127.0.0.1:10001")
	second := neutron.New(nil, nil).WithProxy("socks5://127.0.0.1:10002")
	first.SetProxy("")
	if first.Proxy != "" || second.Proxy != "socks5://127.0.0.1:10002" {
		t.Fatal("proxy leaked between commands")
	}
	if current := fmt.Sprintf("%p/%p", neutronhttp.DefaultOption.Proxy, neutronhttp.DefaultTransport.Proxy); current != original {
		t.Fatal("command changed Neutron process defaults")
	}
}
