//go:build full

package playwright

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// consoleHookJS is injected via EvalOnNewDocument so it captures ALL console
// calls from the very first script execution, including during page load.
const consoleHookJS = `(function() {
	window.__consoleBuffer = [];
	var orig = {};
	['log','warn','error','info','debug','trace','dir'].forEach(function(type) {
		orig[type] = console[type];
		console[type] = function() {
			window.__consoleBuffer.push({
				type: type,
				text: Array.from(arguments).map(function(a) {
					if (typeof a === 'object') { try { return JSON.stringify(a); } catch(e) { return String(a); } }
					return String(a);
				}).join(' '),
				time: new Date().toISOString()
			});
			orig[type].apply(console, arguments);
		};
	});
})();`

// ---------------------------------------------------------------------------
// console
// ---------------------------------------------------------------------------

type consoleEntry struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Time string `json:"time"`
}

func (c *Command) execConsole(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright console: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	if len(args) > 1 && args[1] == "--clear" {
		return sess.withPage(ctx, func(page *rod.Page) (string, error) {
			if _, err := page.Eval(`() => { window.__consoleBuffer = []; }`); err != nil {
				return "", fmt.Errorf("playwright console --clear: %w", err)
			}
			return fmt.Sprintf("Console cleared on session %q", sess.Name), nil
		})
	}

	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		res, err := page.Eval(`() => window.__consoleBuffer || []`)
		if err != nil {
			return "", fmt.Errorf("playwright console: %w", err)
		}
		var entries []consoleEntry
		if err := res.Value.Unmarshal(&entries); err != nil || len(entries) == 0 {
			return "No console messages", nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Console messages (%d):\n", len(entries)))
		for _, ev := range entries {
			sb.WriteString(fmt.Sprintf("  [%s] %s\n", ev.Type, ev.Text))
		}
		return sb.String(), nil
	})
}

// ---------------------------------------------------------------------------
// cookie-list
// ---------------------------------------------------------------------------

func (c *Command) execCookieList(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright cookie-list: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		cookies, err := page.Cookies(nil)
		if err != nil {
			return "", fmt.Errorf("playwright cookie-list: %w", err)
		}
		if len(cookies) == 0 {
			return "No cookies", nil
		}
		data, _ := json.MarshalIndent(cookies, "", "  ")
		return fmt.Sprintf("Cookies (%d):\n%s", len(cookies), string(data)), nil
	})
}

// ---------------------------------------------------------------------------
// cookie-get
// ---------------------------------------------------------------------------

func (c *Command) execCookieGet(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright cookie-get: usage: playwright cookie-get <session> <name>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	name := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		cookies, err := page.Cookies(nil)
		if err != nil {
			return "", fmt.Errorf("playwright cookie-get: %w", err)
		}
		for _, ck := range cookies {
			if ck.Name == name {
				data, _ := json.MarshalIndent(ck, "", "  ")
				return string(data), nil
			}
		}
		return fmt.Sprintf("Cookie %q not found", name), nil
	})
}

// ---------------------------------------------------------------------------
// cookie-set
// ---------------------------------------------------------------------------

func (c *Command) execCookieSet(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright cookie-set: usage: playwright cookie-set <session> <name=value> [name=value...]")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		info, _ := page.Info()
		pageURL := ""
		if info != nil {
			pageURL = info.URL
		}
		var cookies []*proto.NetworkCookieParam
		for _, pair := range args[1:] {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) != 2 {
				continue
			}
			cookies = append(cookies, &proto.NetworkCookieParam{
				Name:  kv[0],
				Value: kv[1],
				URL:   pageURL,
			})
		}
		if len(cookies) == 0 {
			return "", fmt.Errorf("playwright cookie-set: no valid name=value pairs")
		}
		if err := page.SetCookies(cookies); err != nil {
			return "", fmt.Errorf("playwright cookie-set: %w", err)
		}
		return fmt.Sprintf("Set %d cookie(s)", len(cookies)), nil
	})
}

// ---------------------------------------------------------------------------
// cookie-delete
// ---------------------------------------------------------------------------

func (c *Command) execCookieDelete(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright cookie-delete: usage: playwright cookie-delete <session> <name>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	name := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		cookies, err := page.Cookies(nil)
		if err != nil {
			return "", fmt.Errorf("playwright cookie-delete: %w", err)
		}
		found := false
		for _, ck := range cookies {
			if ck.Name == name {
				_ = proto.NetworkDeleteCookies{
					Name:   ck.Name,
					Domain: ck.Domain,
				}.Call(page)
				found = true
			}
		}
		if !found {
			return fmt.Sprintf("Cookie %q not found", name), nil
		}
		return fmt.Sprintf("Deleted cookie %q", name), nil
	})
}

// ---------------------------------------------------------------------------
// cookie-clear
// ---------------------------------------------------------------------------

func (c *Command) execCookieClear(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright cookie-clear: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		cookies, _ := page.Cookies(nil)
		for _, ck := range cookies {
			_ = proto.NetworkDeleteCookies{
				Name:   ck.Name,
				Domain: ck.Domain,
			}.Call(page)
		}
		return fmt.Sprintf("Cleared %d cookie(s)", len(cookies)), nil
	})
}

// ---------------------------------------------------------------------------
// Web Storage helpers (localStorage and sessionStorage)
// ---------------------------------------------------------------------------

func (c *Command) execWebStorageList(ctx context.Context, args []string, storageType string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright %s-list: session name required", storageType)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		js := fmt.Sprintf(`() => {
			const s = %s;
			const items = {};
			for (let i = 0; i < s.length; i++) {
				const k = s.key(i);
				items[k] = s.getItem(k);
			}
			return items;
		}`, storageType)
		res, err := page.Eval(js)
		if err != nil {
			return "", fmt.Errorf("playwright %s-list: %w", storageType, err)
		}
		var items map[string]string
		if err := res.Value.Unmarshal(&items); err != nil || len(items) == 0 {
			return fmt.Sprintf("%s: empty", storageType), nil
		}
		data, _ := json.MarshalIndent(items, "", "  ")
		return fmt.Sprintf("%s (%d items):\n%s", storageType, len(items), string(data)), nil
	})
}

func (c *Command) execWebStorageGet(ctx context.Context, args []string, storageType string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright %s-get: usage: playwright %s-get <session> <key>", storageType, storageType)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	key := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		js := fmt.Sprintf(`(key) => %s.getItem(key)`, storageType)
		res, err := page.Eval(js, key)
		if err != nil {
			return "", fmt.Errorf("playwright %s-get: %w", storageType, err)
		}
		if res.Value.Nil() {
			return fmt.Sprintf("%s[%q] = null", storageType, key), nil
		}
		return fmt.Sprintf("%s[%q] = %s", storageType, key, res.Value.Str()), nil
	})
}

func (c *Command) execWebStorageSet(ctx context.Context, args []string, storageType string) (string, error) {
	if len(args) < 3 {
		return "", fmt.Errorf("playwright %s-set: usage: playwright %s-set <session> <key> <value>", storageType, storageType)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	key := args[1]
	value := strings.Join(args[2:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		js := fmt.Sprintf(`(key, value) => { %s.setItem(key, value); return true; }`, storageType)
		if _, err := page.Eval(js, key, value); err != nil {
			return "", fmt.Errorf("playwright %s-set: %w", storageType, err)
		}
		return fmt.Sprintf("%s[%q] = %q", storageType, key, value), nil
	})
}

func (c *Command) execWebStorageDelete(ctx context.Context, args []string, storageType string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright %s-delete: usage: playwright %s-delete <session> <key>", storageType, storageType)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	key := args[1]
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		js := fmt.Sprintf(`(key) => { %s.removeItem(key); return true; }`, storageType)
		if _, err := page.Eval(js, key); err != nil {
			return "", fmt.Errorf("playwright %s-delete: %w", storageType, err)
		}
		return fmt.Sprintf("Deleted %s[%q]", storageType, key), nil
	})
}

func (c *Command) execWebStorageClear(ctx context.Context, args []string, storageType string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright %s-clear: session name required", storageType)
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		js := fmt.Sprintf(`() => { const n = %s.length; %s.clear(); return n; }`, storageType, storageType)
		res, err := page.Eval(js)
		if err != nil {
			return "", fmt.Errorf("playwright %s-clear: %w", storageType, err)
		}
		count := 0
		if !res.Value.Nil() {
			count = res.Value.Int()
		}
		return fmt.Sprintf("Cleared %d %s item(s)", count, storageType), nil
	})
}

// ---------------------------------------------------------------------------
// state-save / state-load
// ---------------------------------------------------------------------------

func (c *Command) execStateSave(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright state-save: usage: playwright state-save <session> <file>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	outPath := resolvePath(c.workDir, args[1])
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := saveStorageState(page, outPath); err != nil {
			return "", fmt.Errorf("playwright state-save: %w", err)
		}
		return fmt.Sprintf("State saved: %s", outPath), nil
	})
}

func (c *Command) execStateLoad(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright state-load: usage: playwright state-load <session> <file>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	inPath := resolvePath(c.workDir, args[1])
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := loadStorageState(page, inPath); err != nil {
			return "", fmt.Errorf("playwright state-load: %w", err)
		}
		return fmt.Sprintf("State loaded: %s", inPath), nil
	})
}
