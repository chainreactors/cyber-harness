//go:build ignore

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	en "github.com/chainreactors/utils/encode"
	"sigs.k8s.io/yaml"
)

var (
	templatePath string
	resultPath   string
	embedMode    bool
)

func deflateCompress(input []byte) []byte {
	return en.MustDeflateCompress(input)
}

func encode(input []byte) string {
	return en.Base64Encode(deflateCompress(input))
}

func loadYAMLFile(filename string) []byte {
	bs, err := os.ReadFile(filepath.Join(templatePath, filename))
	if err != nil {
		panic(err)
	}
	return deflateCompress(bs)
}

func walkFiles(dir string) []string {
	var files []string
	root := filepath.Join(templatePath, dir)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		panic(err)
	}
	sort.Strings(files)
	return files
}

func loadRawFiles(dir string) []byte {
	data := make(map[string]string)
	for _, file := range walkFiles(dir) {
		bs, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		data[strings.TrimSuffix(filepath.Base(file), ".rule")] = string(bs)
	}
	content, err := yaml.Marshal(data)
	if err != nil {
		panic(err)
	}
	return deflateCompress(content)
}

func recuLoadPoc(dir string) []byte {
	var pocs []interface{}
	for _, file := range walkFiles(dir) {
		bs, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		var tmp interface{}
		if err := yaml.Unmarshal(bs, &tmp); err != nil {
			panic(fmt.Sprintf("%s: %v", file, err))
		}
		if tmp != nil {
			pocs = append(pocs, tmp)
		}
	}
	content, err := yaml.Marshal(pocs)
	if err != nil {
		panic(err)
	}
	return deflateCompress(content)
}

// loadZombieTemplates collects neutron POCs whose info.zombie field is non-empty
// and emits them as a yaml-encoded template list — the format expected by
// zombie/pkg/loader.go LoadTemplates which calls yaml.Unmarshal into
// []*templates.Template.
func loadZombieTemplates(dir string) []byte {
	var pocs []interface{}
	for _, file := range walkFiles(dir) {
		bs, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		var tmp map[string]interface{}
		if err := yaml.Unmarshal(bs, &tmp); err != nil {
			panic(fmt.Sprintf("%s: %v", file, err))
		}
		if tmp == nil {
			continue
		}
		info, ok := tmp["info"].(map[string]interface{})
		if !ok {
			continue
		}
		zombie, _ := info["zombie"].(string)
		if strings.TrimSpace(zombie) == "" {
			continue
		}
		pocs = append(pocs, tmp)
	}
	content, err := yaml.Marshal(pocs)
	if err != nil {
		panic(err)
	}
	return deflateCompress(content)
}

func recuLoadFinger(dir string) []byte {
	var items []interface{}
	for _, file := range walkFiles(dir) {
		bs, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		var tmp interface{}
		if err := yaml.Unmarshal(bs, &tmp); err != nil {
			panic(fmt.Sprintf("%s: %v", file, err))
		}
		if tmp == nil {
			continue
		}
		fingers, ok := tmp.([]interface{})
		if !ok {
			panic(fmt.Sprintf("%s: expected finger list", file))
		}
		parentDir := filepath.Base(filepath.Dir(file))
		protocol := "http"
		if strings.Contains(filepath.ToSlash(dir), "/socket") {
			protocol = "tcp"
		}
		for i, finger := range fingers {
			f, ok := finger.(map[string]interface{})
			if !ok {
				panic(fmt.Sprintf("%s: expected finger object", file))
			}
			if f["protocol"] == nil || f["protocol"] == "" {
				f["protocol"] = protocol
			}
			f["link"] = ""
			f["tag"] = []string{parentDir}
			fingers[i] = f
		}
		items = append(items, fingers...)
	}
	content, err := json.Marshal(items)
	if err != nil {
		panic(err)
	}
	return deflateCompress(content)
}

func parser(key string) []byte {
	switch key {
	case "socket":
		return recuLoadFinger("fingers/socket")
	case "http":
		return recuLoadFinger("fingers/http")
	case "fingerprinthub_web":
		return deflateCompress([]byte("[]"))
	case "fingerprinthub_service":
		return deflateCompress([]byte("[]"))
	case "port":
		return loadYAMLFile("port.yaml")
	case "workflow":
		return loadYAMLFile("workflows.yaml")
	case "neutron":
		return recuLoadPoc("neutron")
	case "spray_rule":
		return loadRawFiles("spray/rule")
	case "spray_common":
		return loadYAMLFile("spray/common.yaml")
	case "spray_dict":
		return loadRawFiles("spray/dict")
	case "extract":
		return loadYAMLFile("extract.yaml")
	case "zombie_common":
		return loadYAMLFile("zombie/keywords.yaml")
	case "zombie_default":
		return loadYAMLFile("zombie/default.yaml")
	case "zombie_rule":
		return loadRawFiles("zombie/rule")
	case "zombie_template":
		return loadZombieTemplates("neutron")
	default:
		panic("illegal key: " + key)
	}
}

func toVarName(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if len(p) > 0 {
			if i == 0 {
				parts[i] = strings.ToLower(p[:1]) + p[1:]
			} else {
				parts[i] = strings.ToUpper(p[:1]) + p[1:]
			}
		}
	}
	return strings.Join(parts, "") + "Data"
}

func main() {
	flag.StringVar(&templatePath, "t", ".", "templates repo path")
	flag.StringVar(&resultPath, "o", "template.go", "result filename")
	need := flag.String("need", "aiscan", "aiscan or comma-separated template keys")
	flag.BoolVar(&embedMode, "embed", false, "use go:embed for binary data (requires Go 1.16+)")
	flag.Parse()

	var needs []string
	if *need == "aiscan" {
		needs = []string{
			"http", "socket", "fingerprinthub_web", "fingerprinthub_service",
			"port", "extract", "workflow", "neutron",
			"spray_rule", "spray_dict", "spray_common",
			"zombie_common", "zombie_default", "zombie_rule", "zombie_template",
		}
	} else {
		needs = strings.Split(*need, ",")
	}

	if embedMode {
		generateEmbed(needs)
	} else {
		generateLegacy(needs)
	}
}

func generateLegacy(needs []string) {
	var b strings.Builder
	b.WriteString("// Code generated by templates_gen.go; DO NOT EDIT.\n\n")
	b.WriteString("package resources\n\n")
	b.WriteString("import \"github.com/chainreactors/utils/encode\"\n\n")
	b.WriteString("func loadEmbeddedConfig(typ string) []byte {\n")
	for _, key := range needs {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		b64 := en.Base64Encode(parser(key))
		b.WriteString(fmt.Sprintf("\tif typ == %q {\n", key))
		b.WriteString(fmt.Sprintf("\t\treturn encode.MustDeflateDeCompress(encode.Base64Decode(%q))\n", b64))
		b.WriteString("\t}\n")
	}
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n")

	if err := os.WriteFile(resultPath, []byte(b.String()), 0644); err != nil {
		panic(err)
	}
	fmt.Println("generate template.go (legacy) successfully")
}

func generateEmbed(needs []string) {
	dataDir := filepath.Join(filepath.Dir(resultPath), "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		panic(fmt.Sprintf("create data dir: %v", err))
	}

	var embedDecls strings.Builder
	var loadBody strings.Builder

	for _, key := range needs {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		data := parser(key)

		binFile := key + ".bin"
		binPath := filepath.Join(dataDir, binFile)
		if err := os.WriteFile(binPath, data, 0644); err != nil {
			panic(fmt.Sprintf("write %s: %v", binPath, err))
		}
		fmt.Printf("  embed: %s (%d bytes)\n", binFile, len(data))

		varName := toVarName(key)
		embedDecls.WriteString(fmt.Sprintf("//go:embed data/%s\nvar %s []byte\n\n", binFile, varName))
		loadBody.WriteString(fmt.Sprintf("\tif typ == %q {\n", key))
		loadBody.WriteString(fmt.Sprintf("\t\treturn encode.MustDeflateDeCompress(%s)\n", varName))
		loadBody.WriteString("\t}\n")
	}

	var b strings.Builder
	b.WriteString("// Code generated by templates_gen.go; DO NOT EDIT.\n\n")
	b.WriteString("package resources\n\n")
	b.WriteString("import (\n")
	b.WriteString("\t_ \"embed\"\n\n")
	b.WriteString("\t\"github.com/chainreactors/utils/encode\"\n")
	b.WriteString(")\n\n")
	b.WriteString(embedDecls.String())
	b.WriteString("func loadEmbeddedConfig(typ string) []byte {\n")
	b.WriteString(loadBody.String())
	b.WriteString("\treturn nil\n")
	b.WriteString("}\n")

	if err := os.WriteFile(resultPath, []byte(b.String()), 0644); err != nil {
		panic(err)
	}
	fmt.Println("generate template.go (embed) successfully")
}
