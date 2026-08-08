package commands

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coretool "github.com/chainreactors/aiscan/core/tool"
)

const (
	shellCommandAdapterMarkerEnv     = "AISCAN_SHELL_COMMAND"
	shellCommandAdapterEndpointEnv   = "AISCAN_SHELL_COMMAND_ENDPOINT"
	shellCommandAdapterCommandEnv    = "AISCAN_SHELL_COMMAND_COMMAND"
	shellCommandAdapterExecutableEnv = "AISCAN_SHELL_COMMAND_EXECUTABLE"
	shellCommandAdapterContextEnv    = "AISCAN_SHELL_COMMAND_CONTEXT"

	shellCommandAdapterProtocolVersion = 1
	shellCommandAdapterChunkSize       = 32 << 10
	shellCommandAdapterMaxFrameSize    = 1 << 20
	shellCommandAdapterDialTimeout     = 5 * time.Second
)

func init() {
	if code, ok := runShellCommandProxyIfRequested(); ok {
		os.Exit(code)
	}
}

type shellCommandAdapterFrame struct {
	Type      string   `json:"type"`
	Version   int      `json:"version,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	Dir       string   `json:"dir,omitempty"`
	ContextID string   `json:"context_id,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	ExitCode  int      `json:"exit_code,omitempty"`
}

type shellCommandAdapter struct {
	registry   *CommandRegistry
	executable string
	runtimeDir string
	endpoint   string
	listener   net.Listener
	cancel     context.CancelFunc

	mu           sync.Mutex
	aliases      map[string]string
	contexts     map[string]context.Context
	nextContext  uint64
	connections  map[net.Conn]struct{}
	wg           sync.WaitGroup
	shutdownOnce sync.Once
	cleanupOnce  sync.Once
}

func newShellCommandAdapter(registry *CommandRegistry) (*shellCommandAdapter, error) {
	if registry == nil {
		return nil, fmt.Errorf("shell command adapter requires a registry")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve shell command executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve shell command executable path: %w", err)
	}
	if err := cleanupStaleShellCommandAdapterRuntime(); err != nil {
		return nil, err
	}

	root := shellCommandAdapterRuntimeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create shell command runtime root: %w", err)
	}
	_ = os.Chmod(root, 0o700)
	runtimeDir, err := os.MkdirTemp(root, strconv.Itoa(os.Getpid())+"-")
	if err != nil {
		return nil, fmt.Errorf("create shell command runtime: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(runtimeDir) }
	_ = os.Chmod(runtimeDir, 0o700)
	endpoint := shellCommandAdapterEndpoint(runtimeDir)
	listener, err := listenShellCommandAdapter(endpoint)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("listen for shell commands: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &shellCommandAdapter{
		registry: registry, executable: executable, runtimeDir: runtimeDir,
		endpoint: endpoint, listener: listener, cancel: cancel,
		aliases: make(map[string]string), contexts: make(map[string]context.Context),
		connections: make(map[net.Conn]struct{}),
	}
	adapter.wg.Add(1)
	go adapter.accept(ctx)
	return adapter, nil
}

func shellCommandAdapterRuntimeRoot() string {
	return filepath.Join(os.TempDir(), "aiscan-shell-commands")
}

func cleanupStaleShellCommandAdapterRuntime() error {
	root := shellCommandAdapterRuntimeRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read shell command runtime root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pidText, _, ok := strings.Cut(entry.Name(), "-")
		pid, parseErr := strconv.Atoi(pidText)
		if !ok || parseErr != nil || pid <= 0 {
			continue
		}
		if !shellCommandAdapterProcessAlive(pid) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
	return nil
}

func (b *shellCommandAdapter) accept(ctx context.Context) {
	defer b.wg.Done()
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		b.mu.Lock()
		b.connections[conn] = struct{}{}
		b.wg.Add(1)
		b.mu.Unlock()
		go b.handle(ctx, conn)
	}
}

func (b *shellCommandAdapter) handle(parent context.Context, conn net.Conn) {
	defer b.wg.Done()
	defer func() {
		_ = conn.Close()
		b.mu.Lock()
		delete(b.connections, conn)
		b.mu.Unlock()
	}()

	reader := bufio.NewReader(conn)
	header, err := readShellCommandAdapterFrame(reader)
	if err != nil {
		return
	}
	writer := &shellCommandAdapterFrameWriter{writer: conn}
	if header.Type != "request" || header.Version != shellCommandAdapterProtocolVersion {
		_, _ = io.WriteString(&shellCommandAdapterStreamWriter{writer: writer, frameType: "stderr"}, "unsupported shell command protocol\n")
		_ = writer.write(shellCommandAdapterFrame{Type: "final", ExitCode: 125})
		return
	}
	command, ok := b.registry.Get(header.Command)
	if !ok || command.Run == nil {
		message := "unknown in-memory command: " + header.Command
		_, _ = io.WriteString(&shellCommandAdapterStreamWriter{writer: writer, frameType: "stderr"}, message+"\n")
		_ = writer.write(shellCommandAdapterFrame{Type: "final", ExitCode: 127})
		return
	}
	baseCtx, ok := b.context(header.ContextID)
	if !ok {
		_, _ = io.WriteString(&shellCommandAdapterStreamWriter{writer: writer, frameType: "stderr"}, "shell command context expired\n")
		_ = writer.write(shellCommandAdapterFrame{Type: "final", ExitCode: 125})
		return
	}

	ctx, cancel := context.WithCancel(baseCtx)
	defer cancel()
	stopParent := context.AfterFunc(parent, cancel)
	defer stopParent()
	stdinReader, stdinWriter := io.Pipe()
	defer stdinReader.Close()
	readDone := make(chan error, 1)
	go func() {
		readDone <- readShellCommandAdapterInput(reader, stdinWriter, cancel)
	}()

	stdout := &shellCommandAdapterStreamWriter{writer: writer, frameType: "stdout"}
	stderr := &shellCommandAdapterStreamWriter{writer: writer, frameType: "stderr"}
	execution := newExecution(nil, command.Name, normalizeNoColor(command.Name, header.Args), header.Dir, nil)
	execution.ID = coretool.InvocationFromContext(ctx).CallID
	execution.StartedAt = time.Now()
	execution.setIO(stdinReader, stdout, stderr)
	_, runErr := command.Run(ctx, execution)
	execution.EndedAt = time.Now()
	_ = stdinReader.Close()
	cancel()

	exitCode := shellCommandAdapterExitCode(runErr)
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		_, _ = io.WriteString(stderr, runErr.Error()+"\n")
	}
	_ = writer.write(shellCommandAdapterFrame{Type: "final", ExitCode: exitCode})
	_ = conn.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
}

func readShellCommandAdapterInput(reader *bufio.Reader, stdin *io.PipeWriter, cancel context.CancelFunc) error {
	stdinOpen := true
	closeStdin := func() {
		if stdinOpen {
			stdinOpen = false
			_ = stdin.Close()
		}
	}
	defer closeStdin()
	for {
		frame, err := readShellCommandAdapterFrame(reader)
		if err != nil {
			cancel()
			return err
		}
		switch frame.Type {
		case "stdin":
			if stdinOpen && len(frame.Data) > 0 {
				if _, err := stdin.Write(frame.Data); err != nil {
					return err
				}
			}
		case "stdin_eof":
			closeStdin()
		case "cancel":
			cancel()
			return context.Canceled
		default:
			cancel()
			return fmt.Errorf("unexpected shell command frame %q", frame.Type)
		}
	}
}

type shellCommandAdapterFrameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *shellCommandAdapterFrameWriter) write(frame shellCommandAdapterFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeShellCommandAdapterFrame(w.writer, frame)
}

type shellCommandAdapterStreamWriter struct {
	writer    *shellCommandAdapterFrameWriter
	frameType string
}

func (w *shellCommandAdapterStreamWriter) Write(data []byte) (int, error) {
	for offset := 0; offset < len(data); {
		end := offset + shellCommandAdapterChunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := w.writer.write(shellCommandAdapterFrame{Type: w.frameType, Data: data[offset:end]}); err != nil {
			return offset, err
		}
		offset = end
	}
	return len(data), nil
}

func shellCommandAdapterExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitCoder interface{ ExitCode() int }
	if errors.As(err, &exitCoder) {
		code := exitCoder.ExitCode()
		if code >= 0 && code <= 255 {
			return code
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return 130
	}
	return 1
}

func writeShellCommandAdapterFrame(writer io.Writer, frame shellCommandAdapterFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > shellCommandAdapterMaxFrameSize {
		return fmt.Errorf("shell command frame too large: %d", len(data))
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if err := writeShellCommandAdapterBytes(writer, size[:]); err != nil {
		return err
	}
	return writeShellCommandAdapterBytes(writer, data)
}

func writeShellCommandAdapterBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func readShellCommandAdapterFrame(reader io.Reader) (shellCommandAdapterFrame, error) {
	var size [4]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return shellCommandAdapterFrame{}, err
	}
	length := binary.BigEndian.Uint32(size[:])
	if length == 0 || length > shellCommandAdapterMaxFrameSize {
		return shellCommandAdapterFrame{}, fmt.Errorf("invalid shell command frame size: %d", length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return shellCommandAdapterFrame{}, err
	}
	var frame shellCommandAdapterFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return shellCommandAdapterFrame{}, err
	}
	return frame, nil
}

func (b *shellCommandAdapter) syncAliases(names []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if !validShellCommandAdapterName(name) {
			continue
		}
		if _, ok := b.aliases[name]; ok {
			continue
		}
		path, err := createShellCommandAdapterAlias(b.executable, b.runtimeDir, name)
		if err != nil {
			return fmt.Errorf("create shell command alias %s: %w", name, err)
		}
		b.aliases[name] = path
	}
	return nil
}

func validShellCommandAdapterName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func (b *shellCommandAdapter) retainContext(ctx context.Context) string {
	if ctx == nil {
		ctx = context.Background()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextContext++
	id := strconv.FormatUint(b.nextContext, 10)
	b.contexts[id] = ctx
	return id
}

func (b *shellCommandAdapter) context(id string) (context.Context, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ctx, ok := b.contexts[id]
	return ctx, ok
}

func (b *shellCommandAdapter) releaseContext(id string) {
	b.mu.Lock()
	delete(b.contexts, id)
	b.mu.Unlock()
}

func (b *shellCommandAdapter) environment(contextID string) []string {
	return []string{
		shellCommandAdapterMarkerEnv + "=1",
		shellCommandAdapterEndpointEnv + "=" + b.endpoint,
		shellCommandAdapterExecutableEnv + "=" + b.executable,
		shellCommandAdapterContextEnv + "=" + contextID,
	}
}

func (b *shellCommandAdapter) shutdown() {
	b.shutdownOnce.Do(func() {
		b.cancel()
		_ = b.listener.Close()
		b.mu.Lock()
		for conn := range b.connections {
			_ = conn.Close()
		}
		b.mu.Unlock()
		b.wg.Wait()
	})
}

func (b *shellCommandAdapter) cleanup() {
	b.cleanupOnce.Do(func() {
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err := os.RemoveAll(b.runtimeDir); err == nil {
				break
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		_ = os.Remove(shellCommandAdapterRuntimeRoot())
	})
}

func (b *shellCommandAdapter) close() {
	b.shutdown()
	b.cleanup()
}

// runShellCommandProxyIfRequested dispatches the process-local PATH shim. The
// marker and command variables are only injected into shim children, so normal
// AIScan and embedding-host startup is unchanged.
func runShellCommandProxyIfRequested() (code int, ok bool) {
	command := strings.TrimSpace(os.Getenv(shellCommandAdapterCommandEnv))
	if os.Getenv(shellCommandAdapterMarkerEnv) != "1" || command == "" {
		return 0, false
	}
	if !validShellCommandAdapterName(command) {
		fmt.Fprintln(os.Stderr, "invalid shell command alias")
		return 126, true
	}
	return runShellCommandAdapterProxy(command, os.Args[1:]), true
}

func runShellCommandAdapterProxy(command string, args []string) int {
	endpoint := os.Getenv(shellCommandAdapterEndpointEnv)
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "AIScan shell command environment is incomplete")
		return 126
	}
	ctx, cancel := context.WithTimeout(context.Background(), shellCommandAdapterDialTimeout)
	conn, err := dialShellCommandAdapter(ctx, endpoint)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect AIScan shell command adapter: %s\n", err)
		return 125
	}
	defer conn.Close()
	cwd, _ := os.Getwd()
	header := shellCommandAdapterFrame{
		Type: "request", Version: shellCommandAdapterProtocolVersion,
		Command: command, Args: append([]string(nil), args...), Dir: cwd,
		ContextID: os.Getenv(shellCommandAdapterContextEnv),
	}
	writer := &shellCommandAdapterFrameWriter{writer: conn}
	if err := writer.write(header); err != nil {
		fmt.Fprintf(os.Stderr, "start AIScan shell command request: %s\n", err)
		return 125
	}
	go streamShellCommandAdapterStdin(writer)

	reader := bufio.NewReader(conn)
	for {
		frame, err := readShellCommandAdapterFrame(reader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read AIScan shell command response: %s\n", err)
			return 125
		}
		switch frame.Type {
		case "stdout":
			_, _ = os.Stdout.Write(frame.Data)
		case "stderr":
			_, _ = os.Stderr.Write(frame.Data)
		case "final":
			flushShellCommandAdapterProxyOutput()
			return frame.ExitCode
		default:
			fmt.Fprintf(os.Stderr, "unexpected AIScan shell command response %q\n", frame.Type)
			return 125
		}
	}
}

func streamShellCommandAdapterStdin(writer *shellCommandAdapterFrameWriter) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		_ = writer.write(shellCommandAdapterFrame{Type: "stdin_eof"})
		return
	}
	buffer := make([]byte, shellCommandAdapterChunkSize)
	for {
		n, readErr := os.Stdin.Read(buffer)
		if n > 0 {
			if err := writer.write(shellCommandAdapterFrame{Type: "stdin", Data: buffer[:n]}); err != nil {
				return
			}
		}
		if readErr != nil {
			_ = writer.write(shellCommandAdapterFrame{Type: "stdin_eof"})
			return
		}
	}
}
