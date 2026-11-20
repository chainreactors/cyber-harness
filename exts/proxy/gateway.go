package proxy

import (
	"fmt"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
	proxytool "github.com/chainreactors/cyber/tools/proxy"
)

func ClashSection() cfg.Section {
	return cfg.Section{
		Key: "clash", New: func() any { return &proxytool.SubscriptionConfig{Refresh: "30m", Strategy: "adaptive"} },
		Secrets: []string{"url", "proxy"},
		Validate: func(value any) error {
			return value.(*proxytool.SubscriptionConfig).Validate()
		},
	}
}

func (e *Extension) Gateway(use string) gateway.Kind {
	return gateway.Kind{
		Config: ClashSection(), Use: use,
		Load: func(scope *extension.Scope, value any) (resource.Handle, error) {
			if value == nil {
				return nil, nil
			}
			if e.resource == nil {
				return nil, fmt.Errorf("proxy must be loaded before its subscription")
			}
			subscription, err := proxytool.NewSubscription(scope.Init(), scope.Lifetime(), e.resource.Hub.State(), *value.(*proxytool.SubscriptionConfig))
			if err != nil {
				// Do not register a typed-nil *Subscription as a resource handle.
				return nil, err
			}
			return subscription, nil
		},
	}
}
