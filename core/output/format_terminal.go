package output

import (
	"fmt"
	"strings"

	"github.com/chainreactors/utils/parsers"
)

func writeGogoMarkdown(sb *strings.Builder, r *parsers.GOGOResult) {
	line := fmt.Sprintf("  - **service** `%s`", r.GetTarget())
	if r.Protocol != "" {
		line += " " + r.Protocol
	}
	if r.Midware != "" {
		line += " — " + TruncateStr(r.Midware, 60)
	}
	sb.WriteString(line + "\n")
}

func writeSprayMarkdown(sb *strings.Builder, r *parsers.SprayResult) {
	var names []string
	for _, f := range r.Frameworks {
		if f.Name != "" {
			names = append(names, f.Name)
		}
	}
	fingers := ""
	if len(names) > 0 {
		fingers = " [" + strings.Join(names, ", ") + "]"
	}
	sb.WriteString(fmt.Sprintf("  - **web** `%s` %d %s%s\n", r.UrlString, r.Status, r.Title, fingers))
}

func writeLootMarkdown(sb *strings.Builder, l *parsers.Loot) {
	sb.WriteString(fmt.Sprintf("  - **%s** `%s` %s\n", l.Kind, l.Target, l.Description))
}
