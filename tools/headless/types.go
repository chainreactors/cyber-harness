//go:build full

package headless

import "github.com/chainreactors/neutron/protocols"

// HeadlessProtocol is the protocol type for headless browser actions.
// Uses a value beyond neutron's built-in range to avoid conflicts.
const HeadlessProtocol protocols.ProtocolType = 100
