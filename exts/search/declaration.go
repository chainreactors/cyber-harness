package search

import (
	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func Declare(resources *resource.Registry) error {
	if _, err := resource.Add[cfg.Section](resources, Section()); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Connection](resources,
		cfg.Connection{Section: ConfigKey, Test: testConnection},
		cfg.Connection{Section: "gateway.tavily", Test: testConnection})
	return err
}
