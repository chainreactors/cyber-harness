//go:build full

package playwright

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-rod/rod/lib/proto"
	"github.com/go-rod/stealth"
)

func (c *Command) execTabList(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright tab-list: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	sess.opMu.Lock()
	defer sess.opMu.Unlock()

	if len(sess.tabs) == 0 {
		return "No tabs", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Tabs (%d):\n", len(sess.tabs)))
	for i, p := range sess.tabs {
		marker := " "
		if i == sess.activeTab {
			marker = "*"
		}
		url := "(blank)"
		title := ""
		if p != nil {
			if info, infoErr := p.Info(); infoErr == nil && info != nil {
				url = info.URL
				title = info.Title
			}
		}
		if title != "" {
			sb.WriteString(fmt.Sprintf("  %s [%d] %s — %s\n", marker, i, url, title))
		} else {
			sb.WriteString(fmt.Sprintf("  %s [%d] %s\n", marker, i, url))
		}
	}
	return sb.String(), nil
}

func (c *Command) execTabNew(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright tab-new: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	url := ""
	if len(args) > 1 {
		url = args[1]
	}

	sess.opMu.Lock()

	browser := sess.Incognito
	if browser == nil {
		sess.opMu.Unlock()
		return "", fmt.Errorf("playwright tab-new: no browser context available")
	}

	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		sess.opMu.Unlock()
		return "", fmt.Errorf("playwright tab-new: create page: %w", err)
	}

	if !sess.attached {
		if _, err := page.EvalOnNewDocument(stealth.JS); err != nil {
			_ = page.Close()
			sess.opMu.Unlock()
			return "", fmt.Errorf("playwright tab-new: stealth: %w", err)
		}
		if _, err := page.EvalOnNewDocument(consoleHookJS); err != nil {
			_ = page.Close()
			sess.opMu.Unlock()
			return "", fmt.Errorf("playwright tab-new: console hook: %w", err)
		}
	}

	if url != "" {
		navPage := page.Context(ctx).Timeout(defaultTimeout)
		if err := navigateTo(navPage, url); err != nil {
			_ = page.Close()
			sess.opMu.Unlock()
			return "", fmt.Errorf("playwright tab-new: navigate: %w", err)
		}
	}

	sess.tabs = append(sess.tabs, page)
	idx := len(sess.tabs) - 1
	sess.activeTab = idx
	sess.Page = page
	sess.opMu.Unlock()

	pageURL := "about:blank"
	if url != "" {
		pageURL = url
	}
	return fmt.Sprintf("New tab [%d]: %s\nActive tab: %d", idx, pageURL, idx), nil
}

func (c *Command) execTabClose(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright tab-close: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	sess.opMu.Lock()
	defer sess.opMu.Unlock()

	if len(sess.tabs) <= 1 {
		return "", fmt.Errorf("playwright tab-close: cannot close the last tab (use close to end the session)")
	}

	idx := sess.activeTab
	if len(args) > 1 {
		i, err := strconv.Atoi(args[1])
		if err != nil || i < 0 || i >= len(sess.tabs) {
			return "", fmt.Errorf("playwright tab-close: invalid tab index %q (0-%d)", args[1], len(sess.tabs)-1)
		}
		idx = i
	}

	page := sess.tabs[idx]
	if !sess.attached {
		_ = page.Close()
	}

	sess.tabs = append(sess.tabs[:idx], sess.tabs[idx+1:]...)

	if idx < sess.activeTab {
		sess.activeTab--
	}
	if sess.activeTab >= len(sess.tabs) {
		sess.activeTab = len(sess.tabs) - 1
	}
	sess.Page = sess.tabs[sess.activeTab]

	return fmt.Sprintf("Closed tab [%d]\nActive tab: %d (%d remaining)", idx, sess.activeTab, len(sess.tabs)), nil
}

func (c *Command) execTabSelect(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright tab-select: usage: playwright tab-select <session> <index>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}

	idx, err := strconv.Atoi(args[1])
	if err != nil {
		return "", fmt.Errorf("playwright tab-select: invalid index %q", args[1])
	}

	sess.opMu.Lock()
	defer sess.opMu.Unlock()

	if idx < 0 || idx >= len(sess.tabs) {
		return "", fmt.Errorf("playwright tab-select: index %d out of range (0-%d)", idx, len(sess.tabs)-1)
	}

	sess.activeTab = idx
	sess.Page = sess.tabs[idx]

	url := "(blank)"
	title := ""
	if info, infoErr := sess.Page.Info(); infoErr == nil && info != nil {
		url = info.URL
		title = info.Title
	}

	return fmt.Sprintf("Active tab: [%d] %s\nTitle: %s", idx, url, title), nil
}
