package extension

import (
	"fmt"
	"reflect"
)

// ServiceID names a typed capability contract. Concrete interfaces remain in
// their owning domain package; the lifecycle kernel only validates topology.
type ServiceID string

// Service is a typed service contract. The kernel stores only its identity and
// verifies that all modules agree on the Go type.
type Service struct {
	ID   string
	Type reflect.Type
}

func ServiceOf[T any](id string) Service { return Service{ID: id, Type: reflect.TypeFor[T]()} }

type Descriptor struct {
	ID          string
	Description string
	Provides    []Service
	Requires    []Service
	Optional    []Service
}

type Describable interface{ Descriptor() Descriptor }

func ValidateServices(descriptors ...Descriptor) error {
	providers := map[ServiceID]string{}
	contracts := map[ServiceID]reflect.Type{}
	for _, d := range descriptors {
		if d.ID == "" {
			return fmt.Errorf("service descriptor requires extension id")
		}
		for _, service := range d.Provides {
			if service.ID == "" {
				return fmt.Errorf("extension %s provides an empty service", d.ID)
			}
			if previous, ok := providers[ServiceID(service.ID)]; ok {
				return fmt.Errorf("service %s provided by both %s and %s", service.ID, previous, d.ID)
			}
			if old := contracts[ServiceID(service.ID)]; old != nil && old != service.Type {
				return fmt.Errorf("service %s has inconsistent Go types", service.ID)
			}
			contracts[ServiceID(service.ID)] = service.Type
			providers[ServiceID(service.ID)] = d.ID
		}
	}
	for _, d := range descriptors {
		for _, service := range d.Requires {
			if old := contracts[ServiceID(service.ID)]; old != nil && old != service.Type {
				return fmt.Errorf("service %s has inconsistent Go types", service.ID)
			}
			contracts[ServiceID(service.ID)] = service.Type
			if _, ok := providers[ServiceID(service.ID)]; !ok {
				optional := false
				for _, candidate := range d.Optional {
					if candidate.ID == service.ID {
						optional = true
						break
					}
				}
				if !optional {
					return fmt.Errorf("extension %s requires unavailable service %s", d.ID, service.ID)
				}
			}
		}
	}
	return nil
}
