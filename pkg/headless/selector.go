//go:build full

package headless

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-rod/rod"
)

// semanticSelectorJS resolves the small, stable locator vocabulary shared by
// the CLI recorder and the headless replay engine. It traverses open shadow
// roots and implements the useful subset of Playwright-style locators without
// coupling templates to Playwright's private selector engine.
const semanticSelectorJS = `(kind, role, name, value, exact, testIdAttribute) => {
	const normalize = text => String(text || '').replace(/\s+/g, ' ').trim();
	const matches = (actual, expected) => {
		actual = normalize(actual);
		expected = normalize(expected);
		return exact ? actual === expected : actual.toLowerCase().includes(expected.toLowerCase());
	};
	const elements = [];
	const visit = root => {
		for (const element of root.querySelectorAll('*')) {
			elements.push(element);
			if (element.shadowRoot) visit(element.shadowRoot);
		}
	};
	visit(document);

	const implicitRole = element => {
		const explicit = element.getAttribute('role');
		if (explicit) return explicit.split(/\s+/)[0].toLowerCase();
		const tag = element.tagName.toLowerCase();
		const type = (element.getAttribute('type') || '').toLowerCase();
		if (tag === 'a' && element.hasAttribute('href')) return 'link';
		if (tag === 'button' || (tag === 'input' && ['button', 'submit', 'reset', 'image'].includes(type))) return 'button';
		if (tag === 'textarea' || element.isContentEditable || (tag === 'input' && !['button', 'submit', 'reset', 'image', 'checkbox', 'radio', 'hidden', 'file'].includes(type))) return 'textbox';
		if (tag === 'input' && type === 'checkbox') return 'checkbox';
		if (tag === 'input' && type === 'radio') return 'radio';
		if (tag === 'select') return element.multiple || element.size > 1 ? 'listbox' : 'combobox';
		if (tag === 'option') return 'option';
		if (/^h[1-6]$/.test(tag)) return 'heading';
		if (tag === 'img') return 'img';
		if (tag === 'ul' || tag === 'ol') return 'list';
		if (tag === 'li') return 'listitem';
		if (tag === 'table') return 'table';
		if (tag === 'tr') return 'row';
		if (tag === 'td') return 'cell';
		if (tag === 'th') return 'columnheader';
		return '';
	};
	const accessibleName = element => {
		const ariaLabel = element.getAttribute('aria-label');
		if (ariaLabel) return normalize(ariaLabel);
		const labelledBy = element.getAttribute('aria-labelledby');
		if (labelledBy) {
			const text = labelledBy.split(/\s+/).map(id => document.getElementById(id)?.textContent || '').join(' ');
			if (normalize(text)) return normalize(text);
		}
		if (element.labels?.length) return normalize(Array.from(element.labels).map(label => label.textContent).join(' '));
		if (element.tagName === 'IMG' && element.alt) return normalize(element.alt);
		if (element.tagName === 'INPUT' && ['button', 'submit', 'reset'].includes((element.type || '').toLowerCase())) return normalize(element.value);
		return normalize(element.getAttribute('title') || element.textContent || '');
	};

	if (kind === 'testid') {
		const attribute = testIdAttribute || 'data-testid';
		return elements.find(element => element.getAttribute(attribute) === value) || null;
	}
	if (kind === 'label') {
		for (const element of elements) {
			if (element.tagName !== 'LABEL' || !matches(element.textContent, value)) continue;
			if (element.control) return element.control;
			const nested = element.querySelector('input,textarea,select,[contenteditable="true"]');
			if (nested) return nested;
		}
		return elements.find(element => element.labels?.length && Array.from(element.labels).some(label => matches(label.textContent, value))) || null;
	}
	if (kind === 'role') {
		return elements.find(element => implicitRole(element) === String(role || '').toLowerCase() && (!name || matches(accessibleName(element), name))) || null;
	}
	if (kind === 'text') {
		const candidates = elements.filter(element => matches(element.innerText || element.textContent, value));
		return candidates.find(element => !Array.from(element.children).some(child => matches(child.innerText || child.textContent, value))) || candidates[0] || null;
	}
	return null;
}`

// ParseSelector converts CSS/XPath and AIScan semantic locator syntax into the
// argument map used by nuclei headless actions.
//
// Supported semantic syntax:
//   - text=Sign in
//   - label=Email
//   - testid=submit
//   - role=button[name="Sign in"]
func ParseSelector(raw string) map[string]string {
	raw = strings.TrimSpace(raw)
	if xpath, ok := strings.CutPrefix(raw, "xpath:"); ok {
		return map[string]string{"by": "xpath", "xpath": xpath}
	}
	for _, prefix := range []struct {
		prefix string
		by     string
		key    string
	}{
		{"text=", "text", "text"},
		{"label=", "label", "label"},
		{"testid=", "testid", "testid"},
	} {
		if value, ok := strings.CutPrefix(raw, prefix.prefix); ok {
			return map[string]string{"by": prefix.by, prefix.key: unquoteSelectorValue(value)}
		}
	}
	if rest, ok := strings.CutPrefix(raw, "role="); ok {
		args := map[string]string{"by": "role"}
		role, attrs, _ := strings.Cut(rest, "[")
		args["role"] = strings.TrimSpace(role)
		attrs = strings.TrimSuffix(attrs, "]")
		for _, attr := range strings.Split(attrs, "][") {
			key, value, found := strings.Cut(attr, "=")
			if found {
				args[strings.TrimSpace(key)] = unquoteSelectorValue(value)
			}
		}
		return args
	}
	return map[string]string{"selector": raw}
}

func unquoteSelectorValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

// FindElement resolves a CLI selector with the same semantics used by replay.
func FindElement(page *rod.Page, selector string, timeout time.Duration) (*rod.Element, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, fmt.Errorf("empty selector")
	}
	return ElementBy(page, ParseSelector(selector), timeout)
}

// ElementBy resolves a nuclei action selector. In addition to nuclei's
// CSS/XPath/regex/search forms, AIScan supports role, label, text, and testid.
func ElementBy(page *rod.Page, data map[string]string, timeout time.Duration) (*rod.Element, error) {
	if timeout <= 0 {
		timeout = defaultActionTimeout
	}
	page = page.Timeout(timeout)
	by := strings.ToLower(strings.TrimSpace(data["by"]))
	switch by {
	case "x", "xpath":
		xpath := data["xpath"]
		if xpath == "" {
			return nil, fmt.Errorf("xpath selector required")
		}
		return page.ElementX(xpath)
	case "js":
		if data["js"] == "" {
			return nil, fmt.Errorf("js selector required")
		}
		return page.ElementByJS(rod.Eval(data["js"]))
	case "r", "regex":
		return page.ElementR(data["selector"], data["regex"])
	case "search":
		result, err := page.Search(data["query"])
		if err != nil {
			return nil, err
		}
		if result.First == nil {
			return nil, fmt.Errorf("no element found for query: %s", data["query"])
		}
		return result.First, nil
	case "role", "label", "text", "testid":
		value := data[by]
		if by == "role" {
			value = data["name"]
		}
		return page.ElementByJS(rod.Eval(
			semanticSelectorJS,
			by,
			data["role"],
			data["name"],
			value,
			strings.EqualFold(data["exact"], "true"),
			data["testid-attribute"],
		))
	default:
		selector := data["selector"]
		if selector == "" {
			return nil, fmt.Errorf("no selector provided")
		}
		return page.Element(selector)
	}
}
