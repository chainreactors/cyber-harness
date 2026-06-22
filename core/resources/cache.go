package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/fingers"
	"gopkg.in/yaml.v3"
)

const cacheTTL = 24 * time.Hour

func cachePath(cyberhubURL, apiKey, kind string) string {
	dir := config.DataSubDir("cache")
	h := sha256.Sum256([]byte(cyberhubURL + "|" + apiKey))
	return filepath.Join(dir, fmt.Sprintf("%s_%s.cache", kind, hex.EncodeToString(h[:8])))
}

func cacheIsFresh(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return time.Since(info.ModTime()) < cacheTTL
}

// --- fingers cache ---

func loadCachedFingers(path string) (fingers.FullFingers, bool) {
	if !cacheIsFresh(path) {
		return fingers.FullFingers{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fingers.FullFingers{}, false
	}
	var items map[string]*fingers.FullFinger
	if err := json.Unmarshal(data, &items); err != nil || len(items) == 0 {
		return fingers.FullFingers{}, false
	}
	return fingers.FullFingers{Items: items}, true
}

func saveCachedFingers(path string, ff fingers.FullFingers) {
	if path == "" {
		return
	}
	data, err := json.Marshal(ff.Items)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

// --- templates cache ---

func loadCachedTemplates(path string) ([]*templates.Template, bool) {
	if !cacheIsFresh(path) {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var tpls []*templates.Template
	if err := yaml.Unmarshal(data, &tpls); err != nil || len(tpls) == 0 {
		return nil, false
	}
	return tpls, true
}

func saveCachedTemplates(path string, tpls []*templates.Template) {
	if path == "" {
		return
	}
	data, err := yaml.Marshal(tpls)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
