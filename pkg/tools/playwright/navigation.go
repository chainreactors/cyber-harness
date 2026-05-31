//go:build browser

package playwright

import (
	"context"
	"fmt"

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
