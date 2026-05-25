package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"
)

func defaultIOANodeName(option *Option) string {
	if option.IOANodeName != "" {
		return option.IOANodeName
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "aiscan-" + hex.EncodeToString(b[:])
	}
	return "aiscan-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
