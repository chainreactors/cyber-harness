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
	commandBridgeMarkerEnv     = "AISCAN_COMMAND_BRIDGE"
	commandBridgeEndpointEnv   = "AISCAN_COMMAND_BRIDGE_ENDPOINT"
	commandBridgeCommandEnv    = "AISCAN_COMMAND_BRIDGE_COMMAND"
	commandBridgeExecutableEnv = "AISCAN_COMMAND_BRIDGE_EXECUTABLE"
	commandBridgeCallIDEnv     = "AISCAN_COMMAND_BRIDGE_CALL_ID"
	commandBridgeSessionEnv    = "AISCAN_COMMAND_BRIDGE_SESSION_ID"
	commandBridgeTurnEnv       = "AISCAN_COMMAND_BRIDGE_TURN_ID"
	commandBridgeEmitterEnv    = "AISCAN_COMMAND_BRIDGE_EMITTER"

	commandBridgeProtocolVersion = 1
	commandBridgeChunkSize       = 32 << 10
	commandBridgeMaxFrameSize    = 1 << 20
	commandBridgeDialTimeout     = 5 * time.Second
)

type commandBridgeFrame struct {
	Type       string              `json:"type"`
	Version    int                 `json:"version,omitempty"`
	Command    string              `json:"command,omitempty"`
	Args       []string            `json:"args,omitempty"`
	Dir        string              `json:"dir,omitempty"`
	Invocation coretool.Invocation `json:"invocation,omitempty"`
	Data       []byte              `json:"data,omitempty"`
	ExitCode   int                 `json:"exit_code,omitempty"`
}

type commandBridge struct {
	registry   *CommandRegistry
	executable string
	runtimeDir string
	endpoint   string
	listener   net.Listener
	cancel     context.CancelFunc

	mu           sync.Mutex
	aliases      map[string]string
	connections  map[net.Conn]struct{}
	wg           sync.WaitGroup
	shutdownOnce sync.Once
	cleanupOnce  sync.Once
}

func newCommandBridge(registry *CommandRegistry) (*commandBridge, error) {
	if registry == nil {
		return nil, fmt.Errorf("command bridge requires a registry")
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve command bridge executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return nil, fmt.Errorf("resolve command bridge executable path: %w", err)
	}
	if err := cleanupStaleCommandBridgeRuntime(); err != nil {
		return nil, err
	}

	root := commandBridgeRuntimeRoot()
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create command bridge runtime root: %w", err)
	}
	_ = os.Chmod(root, 0o700)
	runtimeDir, err := os.MkdirTemp(root, strconv.Itoa(os.Getpid())+"-")
	if err != nil {
		return nil, fmt.Errorf("create command bridge runtime: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(runtimeDir) }
	_ = os.Chmod(runtimeDir, 0o700)
	endpoint := commandBridgeEndpoint(runtimeDir)
	listener, err := listenCommandBridge(endpoint)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("listen command bridge: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &commandBridge{
		registry: registry, executable: executable, runtimeDir: runtimeDir,
		endpoint: endpoint, listener: listener, cancel: cancel,
		aliases: make(map[string]string), connections: make(map[net.Conn]struct{}),
	}
	bridge.wg.Add(1)
	go bridge.accept(ctx)
	return bridge, nil
}

func commandBridgeRuntimeRoot() string {
	return filepath.Join(os.TempDir(), "aiscan-command-bridge")
}

func cleanupStaleCommandBridgeRuntime() error {
	root := commandBridgeRuntimeRoot()
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read command bridge runtime root: %w", err)
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
		if !commandBridgeProcessAlive(pid) {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
	return nil
}

func (b *commandBridge) accept(ctx context.Context) {
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

func (b *commandBridge) handle(parent context.Context, conn net.Conn) {
	defer b.wg.Done()
	defer func() {
		_ = conn.Close()
		b.mu.Lock()
		delete(b.connections, conn)
		b.mu.Unlock()
	}()

	reader := bufio.NewReader(conn)
	header, err := readCommandBridgeFrame(reader)
	if err != nil {
		return
	}
	writer := &commandBridgeFrameWriter{writer: conn}
	if header.Type != "request" || header.Version != commandBridgeProtocolVersion {
		_, _ = io.WriteString(&commandBridgeStreamWriter{writer: writer, frameType: "stderr"}, "unsupported command bridge protocol\n")
		_ = writer.write(commandBridgeFrame{Type: "final", ExitCode: 125})
		return
	}
	command, ok := b.registry.Get(header.Command)
	if !ok || command.Run == nil {
		message := "unknown in-memory command: " + header.Command
		_, _ = io.WriteString(&commandBridgeStreamWriter{writer: writer, frameType: "stderr"}, message+"\n")
		_ = writer.write(commandBridgeFrame{Type: "final", ExitCode: 127})
		return
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	ctx = coretool.ContextWithInvocation(ctx, header.Invocation)
	stdinReader, stdinWriter := io.Pipe()
	defer stdinReader.Close()
	readDone := make(chan error, 1)
	go func() {
		readDone <- readCommandBridgeInput(reader, stdinWriter, cancel)
	}()

	stdout := &commandBridgeStreamWriter{writer: writer, frameType: "stdout"}
	stderr := &commandBridgeStreamWriter{writer: writer, frameType: "stderr"}
	execution := newExecution(nil, command.Name, normalizeNoColor(command.Name, header.Args), header.Dir, nil)
	execution.ID = header.Invocation.CallID
	execution.StartedAt = time.Now()
	execution.setIO(stdinReader, stdout, stderr)
	_, runErr := command.Run(ctx, execution)
	execution.EndedAt = time.Now()
	_ = stdinReader.Close()
	cancel()

	exitCode := commandBridgeExitCode(runErr)
	if runErr != nil && !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		_, _ = io.WriteString(stderr, runErr.Error()+"\n")
	}
	_ = writer.write(commandBridgeFrame{Type: "final", ExitCode: exitCode})
	_ = conn.Close()
	select {
	case <-readDone:
	case <-time.After(time.Second):
	}
}

func readCommandBridgeInput(reader *bufio.Reader, stdin *io.PipeWriter, cancel context.CancelFunc) error {
	stdinOpen := true
	closeStdin := func() {
		if stdinOpen {
			stdinOpen = false
			_ = stdin.Close()
		}
	}
	defer closeStdin()
	for {
		frame, err := readCommandBridgeFrame(reader)
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
			return fmt.Errorf("unexpected command bridge frame %q", frame.Type)
		}
	}
}

type commandBridgeFrameWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *commandBridgeFrameWriter) write(frame commandBridgeFrame) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeCommandBridgeFrame(w.writer, frame)
}

type commandBridgeStreamWriter struct {
	writer    *commandBridgeFrameWriter
	frameType string
}

func (w *commandBridgeStreamWriter) Write(data []byte) (int, error) {
	for offset := 0; offset < len(data); {
		end := offset + commandBridgeChunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := w.writer.write(commandBridgeFrame{Type: w.frameType, Data: data[offset:end]}); err != nil {
			return offset, err
		}
		offset = end
	}
	return len(data), nil
}

func commandBridgeExitCode(err error) int {
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

func writeCommandBridgeFrame(writer io.Writer, frame commandBridgeFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if len(data) > commandBridgeMaxFrameSize {
		return fmt.Errorf("command bridge frame too large: %d", len(data))
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if err := writeCommandBridgeBytes(writer, size[:]); err != nil {
		return err
	}
	return writeCommandBridgeBytes(writer, data)
}

func writeCommandBridgeBytes(writer io.Writer, data []byte) error {
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

func readCommandBridgeFrame(reader io.Reader) (commandBridgeFrame, error) {
	var size [4]byte
	if _, err := io.ReadFull(reader, size[:]); err != nil {
		return commandBridgeFrame{}, err
	}
	length := binary.BigEndian.Uint32(size[:])
	if length == 0 || length > commandBridgeMaxFrameSize {
		return commandBridgeFrame{}, fmt.Errorf("invalid command bridge frame size: %d", length)
	}
	data := make([]byte, int(length))
	if _, err := io.ReadFull(reader, data); err != nil {
		return commandBridgeFrame{}, err
	}
	var frame commandBridgeFrame
	if err := json.Unmarshal(data, &frame); err != nil {
		return commandBridgeFrame{}, err
	}
	return frame, nil
}

func (b *commandBridge) syncAliases(names []string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for _, name := range sorted {
		if !validCommandBridgeName(name) {
			continue
		}
		if _, ok := b.aliases[name]; ok {
			continue
		}
		path, err := createCommandBridgeAlias(b.executable, b.runtimeDir, name)
		if err != nil {
			return fmt.Errorf("create command bridge alias %s: %w", name, err)
		}
		b.aliases[name] = path
	}
	return nil
}

func validCommandBridgeName(name string) bool {
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

func (b *commandBridge) environment(invocation coretool.Invocation) []string {
	return []string{
		commandBridgeMarkerEnv + "=1",
		commandBridgeEndpointEnv + "=" + b.endpoint,
		commandBridgeExecutableEnv + "=" + b.executable,
		commandBridgeCallIDEnv + "=" + invocation.CallID,
		commandBridgeSessionEnv + "=" + invocation.SessionID,
		commandBridgeTurnEnv + "=" + invocation.TurnID,
		commandBridgeEmitterEnv + "=" + invocation.Emitter,
	}
}

func (b *commandBridge) shutdown() {
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

func (b *commandBridge) cleanup() {
	b.cleanupOnce.Do(func() {
		for attempt := 0; attempt < 5; attempt++ {
			if err := os.RemoveAll(b.runtimeDir); err == nil {
				break
			}
			time.Sleep(time.Duration(attempt+1) * 20 * time.Millisecond)
		}
		_ = os.Remove(commandBridgeRuntimeRoot())
	})
}

func (b *commandBridge) close() {
	b.shutdown()
	b.cleanup()
}

// RunCommandBridgeProxyIfRequested detects a bridge wrapper invocation before
// normal CLI parsing. Callers should return normally for code 0 and os.Exit for
// a non-zero code when ok is true.
func RunCommandBridgeProxyIfRequested() (code int, ok bool) {
	command := strings.TrimSpace(os.Getenv(commandBridgeCommandEnv))
	if os.Getenv(commandBridgeMarkerEnv) != "1" || command == "" {
		return 0, false
	}
	if !validCommandBridgeName(command) {
		fmt.Fprintln(os.Stderr, "invalid command bridge alias")
		return 126, true
	}
	return runCommandBridgeProxy(command, os.Args[1:]), true
}

func runCommandBridgeProxy(command string, args []string) int {
	endpoint := os.Getenv(commandBridgeEndpointEnv)
	if endpoint == "" {
		fmt.Fprintln(os.Stderr, "AIScan command bridge environment is incomplete")
		return 126
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandBridgeDialTimeout)
	conn, err := dialCommandBridge(ctx, endpoint)
	cancel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect AIScan command bridge: %s\n", err)
		return 125
	}
	defer conn.Close()
	cwd, _ := os.Getwd()
	header := commandBridgeFrame{
		Type: "request", Version: commandBridgeProtocolVersion,
		Command: command, Args: append([]string(nil), args...), Dir: cwd,
		Invocation: coretool.Invocation{
			WorkDir: cwd, CallID: os.Getenv(commandBridgeCallIDEnv),
			SessionID: os.Getenv(commandBridgeSessionEnv), TurnID: os.Getenv(commandBridgeTurnEnv),
			Emitter: os.Getenv(commandBridgeEmitterEnv),
		},
	}
	writer := &commandBridgeFrameWriter{writer: conn}
	if err := writer.write(header); err != nil {
		fmt.Fprintf(os.Stderr, "start AIScan command bridge request: %s\n", err)
		return 125
	}
	go streamCommandBridgeStdin(writer)

	reader := bufio.NewReader(conn)
	for {
		frame, err := readCommandBridgeFrame(reader)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read AIScan command bridge response: %s\n", err)
			return 125
		}
		switch frame.Type {
		case "stdout":
			_, _ = os.Stdout.Write(frame.Data)
		case "stderr":
			_, _ = os.Stderr.Write(frame.Data)
		case "final":
			flushCommandBridgeProxyOutput()
			return frame.ExitCode
		default:
			fmt.Fprintf(os.Stderr, "unexpected AIScan command bridge response %q\n", frame.Type)
			return 125
		}
	}
}

func streamCommandBridgeStdin(writer *commandBridgeFrameWriter) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		_ = writer.write(commandBridgeFrame{Type: "stdin_eof"})
		return
	}
	buffer := make([]byte, commandBridgeChunkSize)
	for {
		n, readErr := os.Stdin.Read(buffer)
		if n > 0 {
			if err := writer.write(commandBridgeFrame{Type: "stdin", Data: buffer[:n]}); err != nil {
				return
			}
		}
		if readErr != nil {
			_ = writer.write(commandBridgeFrame{Type: "stdin_eof"})
			return
		}
	}
}
