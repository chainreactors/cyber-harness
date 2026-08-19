package node

import aop "github.com/chainreactors/aiscan/aop"

func registerExtraNamespaces(mux *aop.NamespaceMux, registrars []func(*aop.NamespaceMux) error) error {
	for _, register := range registrars {
		if register == nil {
			continue
		}
		if err := register(mux); err != nil {
			return err
		}
	}
	return nil
}
