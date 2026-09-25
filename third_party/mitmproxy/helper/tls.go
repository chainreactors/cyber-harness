package helper

import (
	"io"

	ucert "github.com/chainreactors/utils/cert"
)

func GetTlsKeyLogWriter() io.Writer {
	return ucert.KeyLogWriter()
}
