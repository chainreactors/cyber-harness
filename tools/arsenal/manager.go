package arsenal

import (
	_ "embed"
	"fmt"
	"path/filepath"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/crtm/pkg/registry"
)

//go:embed arsenal.yaml
var catalog []byte

// NewManager opens the harness catalog and installation state without writing
// files or accessing the network. Call Prepare to release bundled executables.
func NewManager(directory string, options crtm.ManagerOption) (*crtm.Manager, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("arsenal requires an absolute directory")
	}
	entries, err := registry.ParseYAML(catalog)
	if err != nil {
		return nil, err
	}
	options.Catalog = registry.Merge(entries, options.Catalog)
	options.BinPath, options.ConfigPath = filepath.Join(directory, "bin"), filepath.Join(directory, "cyber.yaml")
	return crtm.NewManager(options)
}
