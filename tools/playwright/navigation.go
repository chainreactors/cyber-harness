//go:build full

package playwright

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

func (c *Command) execReload(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright reload: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := page.Reload(); err != nil {
			return "", fmt.Errorf("playwright reload: %w", err)
		}
		_ = page.WaitStable(waitStableDur)
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return fmt.Sprintf("Reloaded\nCurrent URL: %s", url), nil
	})
}

func (c *Command) execGoBack(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright go-back: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := page.NavigateBack(); err != nil {
			return "", fmt.Errorf("playwright go-back: %w", err)
		}
		_ = page.WaitStable(waitStableDur)
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return fmt.Sprintf("Navigated back\nCurrent URL: %s", url), nil
	})
}

// ---------------------------------------------------------------------------
// set-content
// ---------------------------------------------------------------------------

func (c *Command) execSetContent(ctx context.Context, args []string) (string, error) {
	if len(args) < 2 {
		return "", fmt.Errorf("playwright set-content: usage: playwright set-content <session> <html>")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	html := strings.Join(args[1:], " ")
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := page.SetDocumentContent(html); err != nil {
			return "", fmt.Errorf("playwright set-content: %w", err)
		}
		return fmt.Sprintf("Content set (%d chars)", len(html)), nil
	})
}

// ---------------------------------------------------------------------------
// title
// ---------------------------------------------------------------------------

func (c *Command) execTitle(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright title: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		info, err := page.Info()
		if err != nil {
			return "", fmt.Errorf("playwright title: %w", err)
		}
		return info.Title, nil
	})
}

func (c *Command) execGoForward(ctx context.Context, args []string) (string, error) {
	if len(args) == 0 {
		return "", fmt.Errorf("playwright go-forward: session name required")
	}
	sess, err := c.getSession(args[0])
	if err != nil {
		return "", err
	}
	return sess.withPage(ctx, func(page *rod.Page) (string, error) {
		if err := page.NavigateForward(); err != nil {
			return "", fmt.Errorf("playwright go-forward: %w", err)
		}
		_ = page.WaitStable(waitStableDur)
		info, _ := page.Info()
		url := ""
		if info != nil {
			url = info.URL
		}
		return fmt.Sprintf("Navigated forward\nCurrent URL: %s", url), nil
	})
}
