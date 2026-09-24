package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHiddenConfigurationDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name      string
		directory string
		binaryDir string
		files     []string
		boundary  string
		want      string
		scope     string
	}{
		{name: "current directory", directory: "project", files: []string{"project/.cyber/cyber.yaml"}, want: "project/.cyber/cyber.yaml", scope: "project"},
		{name: "parent files ignored", directory: "project/nested", files: []string{"project/.cyber/cyber.yaml", "cyber.yaml"}, want: "user/.cyber/cyber.yaml"},
		{name: "current directory wins", directory: "project/nested", files: []string{"project/nested/.cyber/cyber.yaml", "project/cyber.yaml"}, want: "project/nested/.cyber/cyber.yaml", scope: "project"},
		{name: "flat file wins in same directory", directory: "project", files: []string{"project/cyber.yaml", "project/.cyber/cyber.yaml"}, want: "project/cyber.yaml", scope: "project"},
		{name: "binary hidden file ignored", directory: "project", files: []string{"bin/.cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml"},
		{name: "binary flat file ignored", directory: "project", files: []string{"bin/cyber.yaml", "bin/.cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml"},
		{name: "project wins over portable", directory: "project", files: []string{"project/.cyber/cyber.yaml", "bin/cyber.yaml"}, want: "project/.cyber/cyber.yaml", scope: "project"},
		{name: "git boundary", directory: "project/nested", files: []string{".cyber/cyber.yaml"}, boundary: "project/.git", want: "user/.cyber/cyber.yaml"},
		{name: "git root file ignored", directory: "project/nested", files: []string{"project/.cyber/cyber.yaml"}, boundary: "project/.git", want: "user/.cyber/cyber.yaml"},
		{name: "home boundary", directory: "user/nested", files: []string{".cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml"},
		{name: "user file loaded once", directory: "user/nested", files: []string{"user/.cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml", scope: "user"},
		{name: "home working directory loaded once", directory: "user", files: []string{"user/.cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml", scope: "user"},
		{name: "portable user file loaded once", directory: "project", binaryDir: "user", files: []string{"user/.cyber/cyber.yaml"}, want: "user/.cyber/cyber.yaml", scope: "user"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := isolatedContext(t)
			root := filepath.Dir(c.Directory)
			c.Directory = filepath.Join(root, filepath.FromSlash(tt.directory))
			if tt.binaryDir != "" {
				c.Executable = filepath.Join(root, tt.binaryDir, "agent.exe")
			}
			if err := os.MkdirAll(c.Directory, 0700); err != nil {
				t.Fatal(err)
			}
			if tt.boundary != "" {
				if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(tt.boundary)), 0700); err != nil {
					t.Fatal(err)
				}
			}
			for _, file := range tt.files {
				putConfig(t, filepath.Join(root, filepath.FromSlash(file)), "llm:\n  model: fixture-model\n")
			}
			o := resolvedFixture(t, c, "")
			want := filepath.Join(root, filepath.FromSlash(tt.want))
			if o.Snapshot.Target != want {
				t.Fatalf("target = %s, want %s", o.Snapshot.Target, want)
			}
			if tt.scope == "" {
				if len(o.Snapshot.Layers) != 0 {
					t.Fatalf("loaded config outside working or user directory: %+v", o.Snapshot.Layers)
				}
				return
			}
			if len(o.Snapshot.Layers) != 1 || o.Snapshot.Layers[0].Scope != tt.scope {
				t.Fatalf("layers = %+v, want one %s layer", o.Snapshot.Layers, tt.scope)
			}
			if o.Model != "fixture-model" || o.Snapshot.Sources["llm.model"] != want {
				t.Fatal("discovered model or its source was not applied")
			}
		})
	}
}

func TestHiddenProjectProfileResolution(t *testing.T) {
	c := isolatedContext(t)
	putConfig(t, c.UserFile(), "llm:\n  active_profile: work\nagent:\n  timeout: 99\n")
	path := filepath.Join(c.Directory, ".cyber", DefaultConfigName)
	putConfig(t, path, "llm:\n  providers:\n    - id: work\n      provider: openai\n      model: project-model\n      api_key: fixture-key\n")
	o := resolvedFixture(t, c, "")
	if o.ActiveProfile != "work" || o.Model != "project-model" || o.APIKey != "fixture-key" || o.Timeout != 99 {
		t.Fatal("hidden project profile was not resolved with the user layer")
	}
	if len(o.Snapshot.Layers) != 2 || o.Snapshot.Sources["llm.model"] != path {
		t.Fatal("hidden project profile source was not retained")
	}
	explicit := resolvedFixture(t, c, filepath.Join(".cyber", DefaultConfigName))
	if explicit.Model != o.Model || explicit.APIKey != o.APIKey || len(explicit.Snapshot.Layers) != 1 || explicit.Timeout == 99 {
		t.Fatal("explicit hidden file did not load independently")
	}
}
