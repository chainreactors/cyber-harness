package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/aop/aopconnect"
)

// Client demonstrates browser-compatible ConnectRPC. The public chat surface
// is still exactly the six generated aop.ChatService methods; ListAgents is the
// existing product REST endpoint used only to discover a participant.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	chat    aopconnect.ChatServiceClient
}

type Agent struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Busy   bool        `json:"busy"`
	Status AgentStatus `json:"status"`
}

type AgentStatus struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	ConfigError string `json:"config_error"`
}

type AskResult struct {
	SessionID string
	AgentID   string
	TurnID    string
	Output    string
	Stop      string
	Usage     *aop.TokenUsage
}

func NewClient(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid AIScan Web URL %q", baseURL)
	}
	httpClient := &http.Client{}
	return &Client{
		baseURL: baseURL,
		token:   strings.TrimSpace(token),
		http:    httpClient,
		chat:    aopconnect.NewChatServiceClient(httpClient, baseURL, connect.WithProtoJSON()),
	}, nil
}

func requestWithToken[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func (c *Client) OpenSession(ctx context.Context, request *aop.OpenSessionRequest) (*aop.OpenSessionResponse, error) {
	response, err := c.chat.OpenSession(ctx, requestWithToken(c.token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (c *Client) RunTurn(ctx context.Context, request *aop.RunTurnRequest) (*aop.RunTurnResponse, error) {
	response, err := c.chat.RunTurn(ctx, requestWithToken(c.token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (c *Client) CancelTurn(ctx context.Context, request *aop.CancelTurnRequest) (*aop.CancelTurnResponse, error) {
	response, err := c.chat.CancelTurn(ctx, requestWithToken(c.token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (c *Client) CloseSession(ctx context.Context, request *aop.CloseSessionRequest) (*aop.CloseSessionResponse, error) {
	response, err := c.chat.CloseSession(ctx, requestWithToken(c.token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (c *Client) WatchEvents(ctx context.Context, request *aop.WatchEventsRequest) (*connect.ServerStreamForClient[aop.WatchEventsResponse], error) {
	return c.chat.WatchEvents(ctx, requestWithToken(c.token, request))
}

func (c *Client) ListEvents(ctx context.Context, request *aop.ListEventsRequest) (*aop.ListEventsResponse, error) {
	response, err := c.chat.ListEvents(ctx, requestWithToken(c.token, request))
	if err != nil {
		return nil, err
	}
	return response.Msg, nil
}

func (c *Client) ListAgents(ctx context.Context) ([]Agent, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/agents", nil)
	if err != nil {
		return nil, err
	}
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	var agents []Agent
	if err := json.NewDecoder(response.Body).Decode(&agents); err != nil {
		return nil, err
	}
	return agents, nil
}

// Ask is convenience only: it composes OpenSession, WatchEvents, RunTurn and
// the terminal-event loop without introducing another wire protocol.
func (c *Client) Ask(ctx context.Context, prompt, requestedAgentID string, onDelta func(string)) (*AskResult, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	agent, err := pickAgent(agents, requestedAgentID)
	if err != nil {
		return nil, err
	}
	sessionID := "session-" + newID()
	opened, err := c.OpenSession(ctx, &aop.OpenSessionRequest{
		RequestId: "open-" + newID(), SessionId: sessionID, Participant: agent.ID,
		Title: "API: " + truncateRunes(prompt, 48),
	})
	if err != nil {
		return nil, fmt.Errorf("open session: %w", err)
	}
	if rejected := opened.GetRejected(); rejected != nil {
		return nil, rejectionError("open session", rejected)
	}

	// A server-streaming Connect call may wait for its first response before the
	// client call returns. Start it concurrently with RunTurn; the server's
	// subscribe-before-replay implementation makes this race-free in both orders.
	type watchResult struct {
		stream *connect.ServerStreamForClient[aop.WatchEventsResponse]
		err    error
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	watchReady := make(chan watchResult, 1)
	go func() {
		stream, watchErr := c.WatchEvents(watchCtx, &aop.WatchEventsRequest{SessionId: sessionID})
		watchReady <- watchResult{stream: stream, err: watchErr}
	}()
	turnID := "turn-" + newID()
	run, err := c.RunTurn(ctx, &aop.RunTurnRequest{
		RequestId: "run-" + newID(), SessionId: sessionID, TurnId: turnID,
		Input: &aop.Message{Id: "message-" + newID(), Role: "user", Content: []*aop.Content{{
			Value: &aop.Content_Text{Text: &aop.TextContent{Text: prompt}},
		}}},
	})
	if err != nil {
		return nil, fmt.Errorf("run turn: %w", err)
	}
	if rejected := run.GetRejected(); rejected != nil {
		return nil, rejectionError("run turn", rejected)
	}
	var watch *connect.ServerStreamForClient[aop.WatchEventsResponse]
	select {
	case result := <-watchReady:
		if result.err != nil {
			return nil, fmt.Errorf("watch events: %w", result.err)
		}
		watch = result.stream
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	result := &AskResult{SessionID: sessionID, AgentID: agent.ID, TurnID: turnID}
	var deltas strings.Builder
	for watch.Receive() {
		event := watch.Msg().GetDelivery().GetEvent()
		if event == nil || event.TurnId != turnID {
			continue
		}
		switch payload := event.Payload.(type) {
		case *aop.Event_MessageDelta:
			text := payload.MessageDelta.GetText()
			deltas.WriteString(text)
			if onDelta != nil && text != "" {
				onDelta(text)
			}
		case *aop.Event_Message:
			if payload.Message.GetRole() == "assistant" {
				if text := messageText(payload.Message); text != "" {
					result.Output = text
				}
			}
		case *aop.Event_TurnEnded:
			result.Stop = payload.TurnEnded.GetStopReason()
			result.Usage = payload.TurnEnded.GetUsage()
			if failure := payload.TurnEnded.GetError(); failure != nil {
				return nil, fmt.Errorf("turn failed: %s: %s", failure.Code, failure.Message)
			}
			if result.Output == "" {
				result.Output = deltas.String()
			}
			return result, nil
		case *aop.Event_Error:
			if payload.Error != nil {
				return nil, fmt.Errorf("turn error: %s", payload.Error.Message)
			}
		}
	}
	if err := watch.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("event stream closed before turn_ended")
}

func rejectionError(operation string, rejected *aop.Rejection) error {
	return fmt.Errorf("%s rejected: %s: %s", operation, rejected.GetCode(), rejected.GetMessage())
}

func messageText(message *aop.Message) string {
	var text strings.Builder
	for _, content := range message.GetContent() {
		text.WriteString(content.GetText().GetText())
	}
	return text.String()
}

func pickAgent(agents []Agent, requestedID string) (Agent, error) {
	if requestedID != "" {
		for _, agent := range agents {
			if agent.ID == requestedID {
				if agent.Status.Provider == "" {
					return Agent{}, fmt.Errorf("agent %q has no LLM provider", requestedID)
				}
				return agent, nil
			}
		}
		return Agent{}, fmt.Errorf("agent %q is not connected", requestedID)
	}
	var busy *Agent
	for index := range agents {
		if agents[index].Status.Provider == "" {
			continue
		}
		if !agents[index].Busy {
			return agents[index], nil
		}
		if busy == nil {
			busy = &agents[index]
		}
	}
	if busy != nil {
		return *busy, nil
	}
	return Agent{}, errors.New("no connected LLM-capable agent")
}

func newID() string {
	value := make([]byte, 16)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func truncateRunes(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}
