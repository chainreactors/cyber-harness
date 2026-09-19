package main

import (
	"fmt"

	cyberdist "github.com/chainreactors/cyber/pkg/aiscan"
	profilepkg "github.com/chainreactors/cyber/pkg/profile"
)

// newCyberProfileFromRequest adapts host-owned declaration resolution to the
// reusable reference distribution constructor.
func newCyberProfileFromRequest(request profilepkg.Request) (profilepkg.Profile, error) {
	if request.Option == nil {
		return nil, fmt.Errorf("cyber profile option is required")
	}
	if request.Option.Resolved == nil {
		resolved, err := defaultSections().ResolveValues(request.Option.Extensions, nil, nil)
		if err != nil {
			return nil, err
		}
		option := *request.Option
		option.Resolved = resolved
		option.Extensions = resolved.Values()
		request.Option = &option
	}
	return cyberdist.New(cyberdist.Request{
		Option: request.Option, ProviderMode: request.ProviderMode,
		Session: request.Session, Logger: request.Logger,
	})
}
