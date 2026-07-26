package scan

import (
	"testing"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/utils/parsers"
)

func TestAggregateStructuredResultKeepsHTTPOriginsSeparate(t *testing.T) {
	result := &output.Result{
		Services: []*parsers.GOGOResult{
			{Ip: "111.63.65.103", Port: "80", Protocol: "http"},
			{Ip: "111.63.65.103", Port: "443", Protocol: "https"},
			{Ip: "111.63.65.103", Port: "icmp", Protocol: "icmp"},
		},
		WebProbes: []*parsers.SprayResult{
			{UrlString: "http://111.63.65.103/admin", Status: 200, Source: parsers.CheckSource},
			{UrlString: "https://111.63.65.103/login", Status: 301, Source: parsers.CheckSource},
		},
	}

	assets := AggregateStructuredResult(result)
	if len(assets) != 3 {
		t.Fatalf("got %d assets, want separate http, https, and icmp services: %#v", len(assets), assets)
	}
	for _, asset := range assets {
		services, paths := 0, 0
		for _, item := range asset.Items {
			switch item.Kind {
			case output.AssetItemService:
				services++
			case output.AssetItemPath:
				paths++
			}
		}
		wantPaths := 1
		if asset.Target == "111.63.65.103:icmp" {
			wantPaths = 0
		}
		if services != 1 || paths != wantPaths {
			t.Fatalf("asset %q has %d services and %d paths, want 1 service and %d paths", asset.Target, services, paths, wantPaths)
		}
	}
}
