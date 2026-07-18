package webstatic

import "embed"

//go:embed all:static
var FS embed.FS
