package cairnrunner

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/ptywire"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

type Config struct {
	ServerURL string
	WSPath    string
	Token     string
	RunnerID  string
	Registry  *commands.CommandRegistry
	DataBus   *eventbus.Bus[output.ToolDataEvent]
	SCO       *output.SCOSidecar
	Logger    telemetry.Logger
	Version   string
}

type Client struct {
	cfg  Config
	bash *commands.BashTool
}

func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("server URL is required")
	}
	if cfg.Registry == nil {
		return nil, fmt.Errorf("command registry is required")
	}
	tool, ok := cfg.Registry.GetTool("bash")
	if !ok {
		return nil, fmt.Errorf("bash tool is not registered")
	}
	bash, ok := tool.(*commands.BashTool)
	if !ok {
		return nil, fmt.Errorf("registered bash tool has unexpected type %T", tool)
	}
	if cfg.WSPath == "" {
		cfg.WSPath = "/ws/runner"
	}
	if cfg.RunnerID == "" {
		cfg.RunnerID, _ = os.Hostname()
	}
	if cfg.Logger == nil {
		cfg.Logger = telemetry.NopLogger()
	}
	return &Client{cfg: cfg, bash: bash}, nil
}

func (c *Client) Run(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := agent.RetryDelay(attempt)
		c.cfg.Logger.Warnf("cairn runner disconnected, retrying in %s: %v", delay, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	wsURL, err := c.websocketURL()
	if err != nil {
		return err
	}
	headers := http.Header{}
	if c.cfg.Token != "" {
		headers.Set("Authorization", "Bearer "+c.cfg.Token)
	}
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, headers)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("dial cairn runner websocket: %w", err)
	}
	defer conn.Close()

	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &session{
		client:  c,
		ctx:     sessionCtx,
		conn:    conn,
		cancels: make(map[uint32]context.CancelFunc),
		writes:  make(map[uint32]*pendingWrite),
	}
	s.router = c.newPTYRouter()
	defer s.router.Close()

	home, _ := os.UserHomeDir()
	if err := s.writeJSON(message{
		T: "hello", RunnerID: c.cfg.RunnerID, Hostname: hostname(),
		OS: runtime.GOOS, Arch: runtime.GOARCH, Version: c.cfg.Version,
		Meta: map[string]any{"home": home, "cores": runtime.NumCPU(), "commands": c.cfg.Registry.Names()},
	}); err != nil {
		return err
	}
	var welcome message
	if err := conn.ReadJSON(&welcome); err != nil {
		return fmt.Errorf("read welcome: %w", err)
	}
	if welcome.T != "welcome" {
		return fmt.Errorf("expected welcome, got %q", welcome.T)
	}

	detach := s.attachEvents()
	defer detach()
	c.cfg.Logger.Infof("cairn runner connected as %s", c.cfg.RunnerID)

	for {
		kind, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		if kind == websocket.BinaryMessage {
			s.handleBinary(data)
			continue
		}
		if err := s.handleText(data); err != nil {
			c.cfg.Logger.Warnf("cairn runner message: %v", err)
		}
	}
}

func (c *Client) websocketURL() (string, error) {
	u, err := url.Parse(strings.TrimRight(c.cfg.ServerURL, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http", "":
		u.Scheme = "ws"
	}
	u.Path = strings.TrimRight(u.Path, "/") + c.cfg.WSPath
	if c.cfg.Token != "" {
		q := u.Query()
		q.Set("token", c.cfg.Token)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) newPTYRouter() *pty.Router {
	manager := c.bash.Manager().Manager
	openers := pty.DefaultOpeners(manager, pty.DefaultSessionTimeout, pty.DefaultEnv())
	return pty.NewRouter(manager, pty.WithOpeners(openers))
}

type pendingWrite struct {
	path string
	data []byte
}

type session struct {
	client  *Client
	ctx     context.Context
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	cancels map[uint32]context.CancelFunc
	writes  map[uint32]*pendingWrite
	router  *pty.Router
}

func (s *session) writeJSON(value any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(value)
}

func (s *session) writeBinary(value []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, value)
}

func (s *session) handleText(data []byte) error {
	var envelope struct {
		T    string `json:"t"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if strings.HasPrefix(envelope.Type, "pty.") {
		var msg ptywire.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			return err
		}
		frame, err := ptywire.ToFrame(msg)
		if err != nil {
			return err
		}
		s.router.Handle(s.ctx, frame, func(out pty.Frame) {
			_ = s.writeJSON(ptywire.FromFrame(out))
		})
		return nil
	}

	var msg message
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	switch msg.T {
	case "req":
		if msg.Method == "write_file" {
			var params fileParams
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				return s.respond(msg.ID, false, nil, err)
			}
			s.mu.Lock()
			s.writes[msg.ID] = &pendingWrite{path: resolvePath(params.Path), data: make([]byte, 0, params.Size)}
			s.mu.Unlock()
			return nil
		}
		go s.handleRequest(msg)
	case "write_end":
		go s.finishWrite(msg.ID)
	case "cancel":
		s.mu.Lock()
		cancel := s.cancels[msg.ID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return nil
}

func (s *session) handleBinary(data []byte) {
	if len(data) < 4 {
		return
	}
	id := binary.BigEndian.Uint32(data[:4])
	s.mu.Lock()
	if pending := s.writes[id]; pending != nil {
		pending.data = append(pending.data, data[4:]...)
	}
	s.mu.Unlock()
}

func (s *session) handleRequest(msg message) {
	switch msg.Method {
	case "exec":
		s.handleExec(msg)
	case "read_file":
		s.handleRead(msg)
	default:
		_ = s.respond(msg.ID, false, nil, fmt.Errorf("unknown method: %s", msg.Method))
	}
}

func (s *session) respond(id uint32, ok bool, result any, err error) error {
	msg := message{T: "res", ID: id, OK: ok, Result: result}
	if err != nil {
		msg.Error = err.Error()
	}
	return s.writeJSON(msg)
}

func (s *session) attachEvents() func() {
	var unsub func()
	if s.client.cfg.DataBus != nil {
		unsub = s.client.cfg.DataBus.Subscribe(func(event output.ToolDataEvent) {
			payload, _ := json.Marshal(event)
			_ = s.writeJSON(message{T: "event", Event: "tool.data", CallID: event.CallID, Payload: payload})
		})
	}
	if s.client.cfg.SCO != nil {
		s.client.cfg.SCO.OnNodes = func(callID string, nodes []json.RawMessage) {
			payload, _ := json.Marshal(map[string]any{"nodes": nodes})
			_ = s.writeJSON(message{T: "event", Event: "tool.sco", CallID: callID, Payload: payload})
		}
	}
	return func() {
		if unsub != nil {
			unsub()
		}
		if s.client.cfg.SCO != nil {
			s.client.cfg.SCO.OnNodes = nil
		}
	}
}

func resolvePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if filepath.IsAbs(path) {
		return path
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, path)
}

func hostname() string {
	name, _ := os.Hostname()
	return name
}
