package client

import (
	ioaclient "github.com/chainreactors/ioa/client"
	"testing"
)

func TestTypedNilDependency(t *testing.T) {
	var client *ioaclient.Client
	if !isNilIOADependency(client) {
		t.Fatal("typed nil was not recognized")
	}
}
