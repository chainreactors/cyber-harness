//go:build full

package playwright

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIntegration_CookieCommands(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>cookie test</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	execString(t, cmd, context.Background(), []string{"open", srv.URL, "--session", "ck", "--timeout", "10"})

	// cookie-list (empty)
	out := execString(t, cmd, context.Background(), []string{"cookie-list", "ck"})
	if !strings.Contains(out, "No cookies") {
		t.Fatalf("expected no cookies, got: %s", out)
	}

	// cookie-set
	out = execString(t, cmd, context.Background(), []string{"cookie-set", "ck", "foo=bar", "baz=qux"})
	if !strings.Contains(out, "Set 2 cookie") {
		t.Fatalf("expected set 2 cookies, got: %s", out)
	}

	// cookie-list (after set)
	out = execString(t, cmd, context.Background(), []string{"cookie-list", "ck"})
	if !strings.Contains(out, "foo") || !strings.Contains(out, "baz") {
		t.Fatalf("expected cookies foo and baz, got: %s", out)
	}

	// cookie-get
	out = execString(t, cmd, context.Background(), []string{"cookie-get", "ck", "foo"})
	if !strings.Contains(out, "bar") {
		t.Fatalf("expected cookie value bar, got: %s", out)
	}

	// cookie-get (missing)
	out = execString(t, cmd, context.Background(), []string{"cookie-get", "ck", "nonexistent"})
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected cookie not found, got: %s", out)
	}

	// cookie-delete
	out = execString(t, cmd, context.Background(), []string{"cookie-delete", "ck", "foo"})
	if !strings.Contains(out, "Deleted") {
		t.Fatalf("expected deleted, got: %s", out)
	}

	// cookie-list (after delete, should only have baz)
	out = execString(t, cmd, context.Background(), []string{"cookie-list", "ck"})
	if strings.Contains(out, "foo") {
		t.Fatalf("foo should be deleted, got: %s", out)
	}

	// cookie-clear
	out = execString(t, cmd, context.Background(), []string{"cookie-clear", "ck"})
	if !strings.Contains(out, "Cleared") {
		t.Fatalf("expected cleared, got: %s", out)
	}

	// cookie-list (after clear)
	out = execString(t, cmd, context.Background(), []string{"cookie-list", "ck"})
	if !strings.Contains(out, "No cookies") {
		t.Fatalf("expected no cookies after clear, got: %s", out)
	}

	execString(t, cmd, context.Background(), []string{"close", "ck"})
}

func TestIntegration_LocalStorageCommands(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>ls test</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	execString(t, cmd, context.Background(), []string{"open", srv.URL, "--session", "ls", "--timeout", "10"})

	// localstorage-list (empty)
	out := execString(t, cmd, context.Background(), []string{"localstorage-list", "ls"})
	if !strings.Contains(out, "empty") {
		t.Fatalf("expected empty localStorage, got: %s", out)
	}

	// localstorage-set
	out = execString(t, cmd, context.Background(), []string{"localstorage-set", "ls", "mykey", "myvalue"})
	if !strings.Contains(out, "mykey") && !strings.Contains(out, "myvalue") {
		t.Fatalf("expected set confirmation, got: %s", out)
	}

	// localstorage-get
	out = execString(t, cmd, context.Background(), []string{"localstorage-get", "ls", "mykey"})
	if !strings.Contains(out, "myvalue") {
		t.Fatalf("expected myvalue, got: %s", out)
	}

	// localstorage-list (after set)
	out = execString(t, cmd, context.Background(), []string{"localstorage-list", "ls"})
	if !strings.Contains(out, "mykey") {
		t.Fatalf("expected mykey in list, got: %s", out)
	}

	// localstorage-delete
	out = execString(t, cmd, context.Background(), []string{"localstorage-delete", "ls", "mykey"})
	if !strings.Contains(out, "Deleted") {
		t.Fatalf("expected deleted, got: %s", out)
	}

	// localstorage-get (after delete)
	out = execString(t, cmd, context.Background(), []string{"localstorage-get", "ls", "mykey"})
	if !strings.Contains(out, "null") {
		t.Fatalf("expected null after delete, got: %s", out)
	}

	// localstorage-set two items, then clear
	execString(t, cmd, context.Background(), []string{"localstorage-set", "ls", "a", "1"})
	execString(t, cmd, context.Background(), []string{"localstorage-set", "ls", "b", "2"})
	out = execString(t, cmd, context.Background(), []string{"localstorage-clear", "ls"})
	if !strings.Contains(out, "Cleared 2") {
		t.Fatalf("expected cleared 2 items, got: %s", out)
	}

	// localstorage-list (after clear)
	out = execString(t, cmd, context.Background(), []string{"localstorage-list", "ls"})
	if !strings.Contains(out, "empty") {
		t.Fatalf("expected empty after clear, got: %s", out)
	}

	execString(t, cmd, context.Background(), []string{"close", "ls"})
}

func TestIntegration_SessionStorageCommands(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>ss test</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	execString(t, cmd, context.Background(), []string{"open", srv.URL, "--session", "ss", "--timeout", "10"})

	// sessionstorage-list (empty)
	out := execString(t, cmd, context.Background(), []string{"sessionstorage-list", "ss"})
	if !strings.Contains(out, "empty") {
		t.Fatalf("expected empty sessionStorage, got: %s", out)
	}

	// sessionstorage-set
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-set", "ss", "sk", "sv"})
	if !strings.Contains(out, "sk") {
		t.Fatalf("expected set confirmation, got: %s", out)
	}

	// sessionstorage-get
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-get", "ss", "sk"})
	if !strings.Contains(out, "sv") {
		t.Fatalf("expected sv, got: %s", out)
	}

	// sessionstorage-list (after set)
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-list", "ss"})
	if !strings.Contains(out, "sk") {
		t.Fatalf("expected sk in list, got: %s", out)
	}

	// sessionstorage-delete
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-delete", "ss", "sk"})
	if !strings.Contains(out, "Deleted") {
		t.Fatalf("expected deleted, got: %s", out)
	}

	// sessionstorage-get (after delete)
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-get", "ss", "sk"})
	if !strings.Contains(out, "null") {
		t.Fatalf("expected null after delete, got: %s", out)
	}

	// sessionstorage-set + clear
	execString(t, cmd, context.Background(), []string{"sessionstorage-set", "ss", "x", "y"})
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-clear", "ss"})
	if !strings.Contains(out, "Cleared 1") {
		t.Fatalf("expected cleared 1 item, got: %s", out)
	}

	// sessionstorage-list (after clear)
	out = execString(t, cmd, context.Background(), []string{"sessionstorage-list", "ss"})
	if !strings.Contains(out, "empty") {
		t.Fatalf("expected empty after clear, got: %s", out)
	}

	execString(t, cmd, context.Background(), []string{"close", "ss"})
}

func TestIntegration_ConsoleCapture(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>
console.log('hello from page');
console.error('oops error');
console.warn('a warning');
</script>
</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	execString(t, cmd, context.Background(), []string{"open", srv.URL, "--session", "con", "--timeout", "10"})

	// Give console capture a moment to collect events
	time.Sleep(500 * time.Millisecond)

	// console (should have auto-captured messages)
	out := execString(t, cmd, context.Background(), []string{"console", "con"})
	if !strings.Contains(out, "hello from page") {
		t.Fatalf("expected 'hello from page' in console output, got: %s", out)
	}
	if !strings.Contains(out, "oops error") {
		t.Fatalf("expected 'oops error' in console output, got: %s", out)
	}
	if !strings.Contains(out, "a warning") {
		t.Fatalf("expected 'a warning' in console output, got: %s", out)
	}

	// Trigger more console output via eval
	execString(t, cmd, context.Background(), []string{"eval", "con", "console.log('from eval')"})
	time.Sleep(300 * time.Millisecond)

	out = execString(t, cmd, context.Background(), []string{"console", "con"})
	if !strings.Contains(out, "from eval") {
		t.Fatalf("expected 'from eval' in console output, got: %s", out)
	}

	// console --clear
	out = execString(t, cmd, context.Background(), []string{"console", "con", "--clear"})
	if !strings.Contains(out, "cleared") || !strings.Contains(out, "cleared") {
		t.Fatalf("expected clear confirmation, got: %s", out)
	}

	// console (after clear, should be empty)
	out = execString(t, cmd, context.Background(), []string{"console", "con"})
	if !strings.Contains(out, "No console messages") {
		t.Fatalf("expected no console messages after clear, got: %s", out)
	}

	execString(t, cmd, context.Background(), []string{"close", "con"})
}

func TestIntegration_LegacyCookiesAlias(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>legacy test</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	execString(t, cmd, context.Background(), []string{"open", srv.URL, "--session", "lg", "--timeout", "10"})

	// Legacy cookies --list
	out := execString(t, cmd, context.Background(), []string{"cookies", "lg", "--list"})
	if !strings.Contains(out, "No cookies") {
		t.Fatalf("expected no cookies, got: %s", out)
	}

	// Legacy cookies --set
	out = execString(t, cmd, context.Background(), []string{"cookies", "lg", "--set", "x=y"})
	if !strings.Contains(out, "Set 1 cookie") {
		t.Fatalf("expected set 1 cookie, got: %s", out)
	}

	// Legacy cookies --clear
	out = execString(t, cmd, context.Background(), []string{"cookies", "lg", "--clear"})
	if !strings.Contains(out, "Cleared") {
		t.Fatalf("expected cleared, got: %s", out)
	}

	execString(t, cmd, context.Background(), []string{"close", "lg"})
}
