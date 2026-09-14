package config

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

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
