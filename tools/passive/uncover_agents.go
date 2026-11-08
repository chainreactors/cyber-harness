package passive

import (
	"fmt"

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

func (e *UncoverEngine) agentFor(src string) (sources.Agent, error) {
	switch src {
	case "fofa":
		return &richFofaAgent{}, nil
	case "hunter":
		return &richHunterAgent{}, nil
	case "shodan":
		return &shodan.Agent{}, nil
	case "shodan-idb":
		return &shodanidb.Agent{}, nil
	case "censys":
		return &censys.Agent{}, nil
	case "quake":
		return &quake.Agent{}, nil
	case "zoomeye":
		return &zoomeye.Agent{}, nil
	case "netlas":
		return &netlas.Agent{}, nil
	case "criminalip":
		return &criminalip.Agent{}, nil
	case "publicwww":
		return &publicwww.Agent{}, nil
	case "hunterhow":
		return &hunterhow.Agent{}, nil
	case "odin":
		return &odin.Agent{}, nil
	case "binaryedge":
		return &binaryedge.Agent{}, nil
	case "onyphe":
		return &onyphe.Agent{}, nil
	case "driftnet":
		return &driftnet.Agent{}, nil
	case "greynoise":
		return &greynoise.Agent{}, nil
	default:
		return nil, fmt.Errorf("uncover: unknown source %q", src)
	}
}
