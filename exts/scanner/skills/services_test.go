package skills

import (
	"io/fs"
	"strings"
	"testing"
)

// The scanner skill tree loads whatever the host configures, so it must not
// name passive's optional services; passive owns its documentation and
// mounts it only when a source is configured.
func TestSkillTreeNamesNoOptionalService(t *testing.T) {
	err := fs.WalkDir(FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		if path == "cyber/okf/easm/passive.md" {
			t.Errorf("%s duplicates the passive extension's playbook", path)
		}
		raw, err := fs.ReadFile(FS(), path)
		if err != nil {
			return err
		}
		text := strings.ToLower(string(raw))
		for _, service := range []string{"fofa", "hunter", "shodan", "censys", "quake", "zoomeye"} {
			if strings.Contains(text, service) {
				t.Errorf("%s names optional service %q", path, service)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
