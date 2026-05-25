//go:build full

package engine

import (
	"github.com/projectdiscovery/uncover/sources"
	"github.com/projectdiscovery/uncover/sources/agent/binaryedge"
	"github.com/projectdiscovery/uncover/sources/agent/censys"
	"github.com/projectdiscovery/uncover/sources/agent/criminalip"
	"github.com/projectdiscovery/uncover/sources/agent/driftnet"
	"github.com/projectdiscovery/uncover/sources/agent/greynoise"
	"github.com/projectdiscovery/uncover/sources/agent/hunterhow"
	"github.com/projectdiscovery/uncover/sources/agent/netlas"
	"github.com/projectdiscovery/uncover/sources/agent/odin"
	"github.com/projectdiscovery/uncover/sources/agent/onyphe"
	"github.com/projectdiscovery/uncover/sources/agent/publicwww"
	"github.com/projectdiscovery/uncover/sources/agent/quake"
	"github.com/projectdiscovery/uncover/sources/agent/shodan"
	"github.com/projectdiscovery/uncover/sources/agent/shodanidb"
	"github.com/projectdiscovery/uncover/sources/agent/zoomeye"
)

var stockAgents = map[string]sources.Agent{
	"shodan":     &shodan.Agent{},
	"shodan-idb": &shodanidb.Agent{},
	"censys":     &censys.Agent{},
	"quake":      &quake.Agent{},
	"zoomeye":    &zoomeye.Agent{},
	"netlas":     &netlas.Agent{},
	"criminalip": &criminalip.Agent{},
	"publicwww":  &publicwww.Agent{},
	"hunterhow":  &hunterhow.Agent{},
	"odin":       &odin.Agent{},
	"binaryedge": &binaryedge.Agent{},
	"onyphe":     &onyphe.Agent{},
	"driftnet":   &driftnet.Agent{},
	"greynoise":  &greynoise.Agent{},
}
