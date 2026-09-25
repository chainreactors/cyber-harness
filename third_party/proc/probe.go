package proc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// Probes are values a caller hands to an attachment. The registry only ever
// calls one and looks at its error; it never learns what readiness means for a
// particular unit. Liveness is a separate probe from readiness on purpose: a
// service that came up and later wedged is not the same as one that never came
// up, and only the caller can tell them apart.

const defaultProbeInterval = 100 * time.Millisecond

// DialProbe reports a unit ready once its address accepts a connection.
func DialProbe(network, address string) func(context.Context) error {
	return retry(func(ctx context.Context) error {
		dialer := net.Dialer{Timeout: time.Second}
		conn, err := dialer.DialContext(ctx, network, address)
		if err != nil {
			return err
		}
		return conn.Close()
	})
}

// FileProbe reports a unit ready once it has created path, which is how a
// process that picks its own port or socket announces where to find it.
func FileProbe(path string) func(context.Context) error {
	return retry(func(context.Context) error {
		_, err := os.Stat(path)
		return err
	})
}

// LineProbe reports a unit ready once a line of its output satisfies match. The
// reader must be one nothing else consumes: a probe that competes with the
// registry's own pump would steal output from the ring buffer.
func LineProbe(r io.Reader, match func(line string) bool) func(context.Context) error {
	return func(ctx context.Context) error {
		lines := make(chan string)
		scanErr := make(chan error, 1)
		go func() {
			defer close(lines)
			scanner := bufio.NewScanner(r)
			scanner.Buffer(make([]byte, 4096), 1<<20)
			for scanner.Scan() {
				select {
				case lines <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
			scanErr <- scanner.Err()
		}()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case err := <-scanErr:
				if err != nil {
					return err
				}
				return fmt.Errorf("output ended before a matching line")
			case line, ok := <-lines:
				if !ok {
					return fmt.Errorf("output ended before a matching line")
				}
				if match(line) {
					return nil
				}
			}
		}
	}
}

// retry polls check until it succeeds or ctx ends. The unit's own deadline
// bounds it, so a probe never needs a timeout of its own.
func retry(check func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(defaultProbeInterval)
		defer ticker.Stop()
		var last error
		for {
			if err := check(ctx); err == nil {
				return nil
			} else {
				last = err
			}
			select {
			case <-ctx.Done():
				if last != nil {
					return fmt.Errorf("%w (last attempt: %v)", ctx.Err(), last)
				}
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}
