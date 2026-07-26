//go:build full

package playwright

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	katanatypes "github.com/projectdiscovery/katana/pkg/engine/headless/types"
	formfill "github.com/projectdiscovery/katana/pkg/utils"
	mapsutil "github.com/projectdiscovery/utils/maps"
)

// execAutofill discovers forms via katana JS, uses katana's FormFillSuggestions
// for smart defaults, applies user overrides, and fills via go-rod.
func (c *Command) execAutofill(ctx context.Context, args []string) (string, error) {
	sessName, formIndex, overrides, err := parseAutofillOpts(args)
	if err != nil {
		return "", err
	}
	sess, err := c.getSession(sessName)
	if err != nil {
		return "", err
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		// Discover forms via katana JS.
		var forms []*katanatypes.HTMLForm
		_ = evalJSON(page, `() => window.getAllForms()`, &forms)

		if len(forms) == 0 {
			return "No forms found on page", nil
		}
		if formIndex < 0 || formIndex >= len(forms) {
			return "", fmt.Errorf("playwright autofill: form index %d out of range (found %d forms)", formIndex, len(forms))
		}

		form := forms[formIndex]

		// Convert katana HTMLElements to formfill types for suggestion generation.
		var formFields []interface{}
		for _, el := range form.Elements {
			if el.TagName == "BUTTON" || el.Type == "submit" || el.Type == "button" {
				continue
			}
			switch strings.ToUpper(el.TagName) {
			case "SELECT":
				formFields = append(formFields, convertToFormSelect(el))
			case "TEXTAREA":
				formFields = append(formFields, convertToFormTextArea(el))
			default:
				formFields = append(formFields, convertToFormInput(el))
			}
		}

		suggestions := formfill.FormFillSuggestions(formFields)
		addSelectSuggestions(page, form, &suggestions)

		// Apply user overrides.
		for k, v := range overrides {
			suggestions.Set(k, v)
		}

		// Fill elements via go-rod using xpath from katana.
		var filled []string
		suggestions.Iterate(func(name, value string) bool {
			el := findFormElement(page, form, name)
			if el == nil {
				return true
			}
			tagRes, tagErr := el.Eval(`() => this.tagName`)
			if tagErr != nil {
				return true
			}
			tag := tagRes.Value.Str()

			switch tag {
			case "SELECT":
				_ = selectOption(el, value)
			case "INPUT":
				typeRes, _ := el.Attribute("type")
				inputType := ""
				if typeRes != nil {
					inputType = *typeRes
				}
				switch inputType {
				case "checkbox", "radio":
					_ = el.Click(proto.InputMouseButtonLeft, 1)
				default:
					// Clear then fill.
					_ = el.SelectAllText()
					_ = el.Input(value)
				}
			default:
				_ = el.SelectAllText()
				_ = el.Input(value)
			}

			filled = append(filled, fmt.Sprintf("%s=%s", name, value))
			return true
		})

		_ = page.WaitStable(waitStableDur)

		return fmt.Sprintf("Autofilled form [%d] with %d fields:\n  %s",
			formIndex, len(filled), strings.Join(filled, "\n  ")), nil
	})
}

// findFormElement locates a rod.Element for a named form field
// using xpath first, then CSS selector, then name attribute.
func findFormElement(page *rod.Page, form *katanatypes.HTMLForm, name string) *rod.Element {
	for _, el := range form.Elements {
		elName := formElementName(el)
		if elName != name {
			continue
		}
		// Try xpath first (most reliable from katana).
		if el.XPath != "" {
			if rodEl, err := page.ElementX(el.XPath); err == nil {
				return rodEl
			}
		}
		// Fallback to CSS selector.
		if el.CSSSelector != "" {
			if rodEl, err := page.Element(el.CSSSelector); err == nil {
				return rodEl
			}
		}
		// Last resort: name attribute selector.
		sel := fmt.Sprintf(`[name="%s"]`, name)
		if rodEl, err := page.Element(sel); err == nil {
			return rodEl
		}
	}
	return nil
}

func addSelectSuggestions(page *rod.Page, form *katanatypes.HTMLForm, suggestions *mapsutil.OrderedMap[string, string]) {
	for _, el := range form.Elements {
		if strings.ToUpper(el.TagName) != "SELECT" {
			continue
		}
		name := formElementName(el)
		if name == "" || suggestions.Has(name) {
			continue
		}
		rodEl := findFormElement(page, form, name)
		if rodEl == nil {
			continue
		}
		if value, ok := firstSelectOptionValue(rodEl); ok {
			suggestions.Set(name, value)
		}
	}
}

func firstSelectOptionValue(el *rod.Element) (string, bool) {
	res, err := el.Eval(`() => {
		const options = Array.from(this.options || []);
		const option =
			options.find((item) => item.selected && !item.disabled && item.value !== "") ||
			options.find((item) => !item.disabled && item.value !== "") ||
			options.find((item) => !item.disabled);
		if (!option) return "";
		return option.value || option.textContent.trim();
	}`)
	if err != nil || res == nil || res.Value.Nil() {
		return "", false
	}
	value := strings.TrimSpace(res.Value.Str())
	return value, value != ""
}

func formElementName(el *katanatypes.HTMLElement) string {
	if el == nil {
		return ""
	}
	if n, ok := el.Attributes["name"]; ok && n != "" {
		return n
	}
	return el.ID
}

// ---------------------------------------------------------------------------
// Conversion helpers: katana HTMLElement to formfill types.
// ---------------------------------------------------------------------------

func convertToFormInput(el *katanatypes.HTMLElement) formfill.FormInput {
	attrs := mapsutil.NewOrderedMap[string, string]()
	for k, v := range el.Attributes {
		if k != "name" && k != "value" && k != "type" {
			attrs.Set(k, v)
		}
	}
	name := formElementName(el)
	return formfill.FormInput{
		Name:       name,
		Type:       el.Type,
		Value:      el.Value,
		Attributes: attrs,
	}
}

func convertToFormSelect(el *katanatypes.HTMLElement) formfill.FormSelect {
	attrs := mapsutil.NewOrderedMap[string, string]()
	for k, v := range el.Attributes {
		if k != "name" {
			attrs.Set(k, v)
		}
	}
	name := formElementName(el)
	return formfill.FormSelect{
		Name:       name,
		Attributes: attrs,
	}
}

func convertToFormTextArea(el *katanatypes.HTMLElement) formfill.FormTextArea {
	attrs := mapsutil.NewOrderedMap[string, string]()
	for k, v := range el.Attributes {
		if k != "name" {
			attrs.Set(k, v)
		}
	}
	name := formElementName(el)
	return formfill.FormTextArea{
		Name:       name,
		Attributes: attrs,
	}
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

func parseAutofillOpts(args []string) (sessName string, formIndex int, overrides map[string]string, err error) {
	overrides = make(map[string]string)

	if len(args) == 0 {
		return "", 0, nil, fmt.Errorf("playwright autofill: session name required")
	}
	sessName = args[0]

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--form":
			if i+1 >= len(args) {
				return "", 0, nil, fmt.Errorf("playwright autofill: --form requires an index")
			}
			i++
			idx, parseErr := strconv.Atoi(args[i])
			if parseErr != nil {
				return "", 0, nil, fmt.Errorf("playwright autofill: --form must be an integer: %w", parseErr)
			}
			if idx < 0 {
				return "", 0, nil, fmt.Errorf("playwright autofill: --form must be >= 0")
			}
			formIndex = idx
		case "--data":
			if i+1 >= len(args) {
				return "", 0, nil, fmt.Errorf("playwright autofill: --data requires key=value pairs")
			}
			i++
			pairs := strings.Split(args[i], ",")
			for _, pair := range pairs {
				pair = strings.TrimSpace(pair)
				if pair == "" {
					continue
				}
				kv := strings.SplitN(pair, "=", 2)
				if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
					return "", 0, nil, fmt.Errorf("playwright autofill: invalid --data pair %q (expected key=value)", pair)
				}
				overrides[strings.TrimSpace(kv[0])] = kv[1]
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", 0, nil, fmt.Errorf("playwright autofill: unknown flag: %s", args[i])
			}
			return "", 0, nil, fmt.Errorf("playwright autofill: unexpected argument: %s", args[i])
		}
	}
	return
}
