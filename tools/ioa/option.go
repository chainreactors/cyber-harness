package ioa

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/ioa/protocols"
)

type localIdentity struct{ ref protocols.NodeRef }

func (i localIdentity) IOABinding() protocols.IdentityBinding {
	return protocols.IdentityBinding{Namespace: "aiscan.memory", Subject: i.ref.URI()}
}

// ConfigFromOption builds the local product's optional IOA instance config.
// Remote profiles may construct Config directly.
func ConfigFromOption(option *cfg.Option) *Config {
	if option == nil || option.IOAURL == "" {
		return nil
	}
	return &Config{
		URL: option.IOAURL, NodeID: option.IOANodeID,
		NodeName: ResolveNodeName(option.IOANodeName), Space: option.Space,
		RegisterCommands: true, AutoRegister: true,
		NodeMeta: map[string]any{"client": "aiscan"},
		Identity: localIdentity{ref: protocols.NodeRef{
			ID: protocols.NewID(), Authority: "memory://aiscan",
		}},
	}
}

func ResolveNodeName(preferred string) string {
	if preferred != "" {
		return preferred
	}
	var value [4]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "aiscan-" + hex.EncodeToString(value[:])
	}
	return "aiscan-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
