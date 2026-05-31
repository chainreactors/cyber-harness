//go:build browser

package playwright

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	katanatypes "github.com/projectdiscovery/katana/pkg/engine/headless/types"
)

// execDiscover calls katana's injected JS to enumerate all interactive
// elements on the current page: forms, buttons, onclick links, event
// listeners (from hooks), and SPA navigated links.
func (c *Command) execDiscover(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright discover: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		var forms []*katanatypes.HTMLForm
		_ = evalJSON(page, `() => window.getAllForms()`, &forms)

		var buttons []*katanatypes.HTMLElement
		_ = evalJSON(page, `() => window.getAllElements("button, input[type='button'], input[type='submit']")`, &buttons)

		var onclickLinks []*katanatypes.HTMLElement
		_ = evalJSON(page, `() => window.getAllElements("a[onclick], [onclick]")`, &onclickLinks)

		// Captured by page-init.js hooks (see execOpen).
		var eventListeners []*katanatypes.EventListener
		_ = evalJSON(page, `() => window.__eventListeners || []`, &eventListeners)

		var navLinks []navigatedLink
		_ = evalJSON(page, `() => window.__navigatedLinks || []`, &navLinks)

		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}

		return formatDiscovery(sess.Name, url, forms, buttons, onclickLinks, eventListeners, navLinks), nil
	})
}

// navigatedLink represents a captured SPA navigation from page-init.js hooks.
type navigatedLink struct {
	URL    string `json:"url"`
	Source string `json:"source"`
}

// evalJSON runs a JS expression and unmarshals its result into out.
// gson.JSON parses lazily and replaces its stored value on the first read,
// so Unmarshal must be the first call against the result — never preceded
// by .Nil()/.Str(), which would consume the raw bytes and make Unmarshal fail.
func evalJSON(page *rod.Page, js string, out interface{}) error {
	res, err := page.Eval(js)
	if err != nil {
		return err
	}
	return res.Value.Unmarshal(out)
}

// ---------------------------------------------------------------------------
// Formatting
// ---------------------------------------------------------------------------

func formatDiscovery(
	sessName, url string,
	forms []*katanatypes.HTMLForm,
	buttons []*katanatypes.HTMLElement,
	onclickLinks []*katanatypes.HTMLElement,
	eventListeners []*katanatypes.EventListener,
	navLinks []navigatedLink,
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
		sb.WriteString("\n")
	}

	// Event listeners (from page-init.js hooks)
	if len(eventListeners) > 0 {
		sb.WriteString(fmt.Sprintf("Event Listeners (%d):\n", len(eventListeners)))
		for _, el := range eventListeners {
			if el.Element == nil {
				continue
			}
			tag := strings.ToLower(el.Element.TagName)
			sel := selectorFor(el.Element)
			text := truncText(el.Element.TextContent, 30)
			if text != "" {
				sb.WriteString(fmt.Sprintf("  - %s on <%s> %q  selector=%q\n",
					el.Type, tag, text, sel))
			} else {
				sb.WriteString(fmt.Sprintf("  - %s on <%s>  selector=%q\n",
					el.Type, tag, sel))
			}
		}
		sb.WriteString("\n")
	}

	// Navigated links (SPA routes, fetch targets, WebSocket URLs)
	if len(navLinks) > 0 {
		sb.WriteString(fmt.Sprintf("Navigated Links (%d):\n", len(navLinks)))
		for _, link := range navLinks {
			sb.WriteString(fmt.Sprintf("  - %s  (source: %s)\n", link.URL, link.Source))
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
