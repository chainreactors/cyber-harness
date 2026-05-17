package cmd

import "github.com/chainreactors/aiscan/pkg/record"

func runViewFile(path, format, output string) error {
	return record.RenderFile(path, format, output)
}
