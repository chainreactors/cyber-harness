//go:build browser

package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	katanatypes "github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// execDiscover calls katana's injected JS to enumerate all interactive
// elements on the current page: forms, buttons, and onclick links.
func (c *Command) execDiscover(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("browser discover: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		// --- Forms ---
		var forms []*katanatypes.HTMLForm
		formsRes, err := page.Eval(`() => window.getAllForms()`)
		if err == nil && !formsRes.Value.Nil() {
			_ = formsRes.Value.Unmarshal(&forms)
		}

		// --- Standalone buttons (outside forms) ---
		var buttons []*katanatypes.HTMLElement
		btnsRes, err := page.Eval(`() => window.getAllElements("button, input[type='button'], input[type='submit']")`)
		if err == nil && !btnsRes.Value.Nil() {
			_ = btnsRes.Value.Unmarshal(&buttons)
		}

		// --- Links with onclick handlers ---
		var onclickLinks []*katanatypes.HTMLElement
		linksRes, err := page.Eval(`() => window.getAllElements("a[onclick], [onclick]")`)
		if err == nil && !linksRes.Value.Nil() {
			_ = linksRes.Value.Unmarshal(&onclickLinks)
		}

		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}

		return formatDiscovery(sess.Name, url, forms, buttons, onclickLinks), nil
	})
}

// execDiscoverJSON is an alternative that returns raw JSON for programmatic use.
func (c *Command) execDiscoverJSON(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 || args[1] != "--json" {
		return c.execDiscover(ctx, args)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		var forms []*katanatypes.HTMLForm
		formsRes, err := page.Eval(`() => window.getAllForms()`)
		if err == nil && !formsRes.Value.Nil() {
			_ = formsRes.Value.Unmarshal(&forms)
		}

		data, _ := json.MarshalIndent(forms, "", "  ")
		return string(data), nil
	})
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func formatDiscovery(
	sessName, url string,
	forms []*katanatypes.HTMLForm,
	buttons []*katanatypes.HTMLElement,
	onclickLinks []*katanatypes.HTMLElement,
) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Session: %s | URL: %s\n\n", sessName, url))

	// Forms
	if len(forms) > 0 {
		sb.WriteString(fmt.Sprintf("Forms (%d):\n", len(forms)))
		for i, f := range forms {
			action := f.Action
			if action == "" {
				action = "(self)"
			}
			method := strings.ToUpper(f.Method)
			if method == "" {
				method = "GET"
			}
			id := f.ID
			tag := "form"
			if id != "" {
				tag += "#" + id
			}
			sb.WriteString(fmt.Sprintf("  [%d] <%s> action=%s method=%s\n", i, tag, action, method))

			for _, el := range f.Elements {
				elType := el.Type
				if elType == "" {
					elType = "text"
				}
				name := el.ID
				if n, ok := el.Attributes["name"]; ok && n != "" {
					name = n
				}
				sel := selectorFor(el)
				sb.WriteString(fmt.Sprintf("      - %s[name=%s] type=%s  selector=%q\n",
					strings.ToLower(el.TagName), name, elType, sel))
			}
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("Forms: none\n\n")
	}

	// Buttons
	if len(buttons) > 0 {
		sb.WriteString(fmt.Sprintf("Buttons (%d):\n", len(buttons)))
		for _, btn := range buttons {
			text := truncText(btn.TextContent, 40)
			sel := selectorFor(btn)
			sb.WriteString(fmt.Sprintf("  - %s %q  selector=%q\n",
				strings.ToLower(btn.TagName), text, sel))
		}
		sb.WriteString("\n")
	}

	// Onclick links
	if len(onclickLinks) > 0 {
		sb.WriteString(fmt.Sprintf("Onclick elements (%d):\n", len(onclickLinks)))
		for _, link := range onclickLinks {
			text := truncText(link.TextContent, 40)
			sel := selectorFor(link)
			sb.WriteString(fmt.Sprintf("  - %s %q  selector=%q\n",
				strings.ToLower(link.TagName), text, sel))
		}
	}

	return sb.String()
}

// selectorFor returns the best available selector for an element,
// preferring cssSelector > xpath > tag#id.
func selectorFor(el *katanatypes.HTMLElement) string {
	if el.CSSSelector != "" {
		return el.CSSSelector
	}
	if el.XPath != "" {
		return "xpath:" + el.XPath
	}
	s := strings.ToLower(el.TagName)
	if el.ID != "" {
		s += "#" + el.ID
	}
	return s
}

func truncText(s string, max int) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
