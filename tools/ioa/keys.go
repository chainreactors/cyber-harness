package ioa

import (
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/ioa/protocols"
)

// ClientKey carries the bound IOA client to the ioa command factory.
var ClientKey = deps.NewKey[protocols.ClientAPI]("ioa.Client")
