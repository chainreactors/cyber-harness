package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	aiscan "github.com/chainreactors/aiscan/aop/aiscan"
)

func main() {
	baseURL := flag.String("url", "http://127.0.0.1:8080", "AIScan Web base URL")
	token := flag.String("token", os.Getenv("AISCAN_WEB_TOKEN"), "AIScan access token")
	agentID := flag.String("agent", os.Getenv("AISCAN_AGENT_ID"), "connected Agent participant ID")
	prompt := flag.String("prompt", "请用一句话介绍你的能力", "natural-language prompt")
	timeout := flag.Duration("timeout", 10*time.Minute, "overall timeout")
	flag.Parse()
	if strings.TrimSpace(*agentID) == "" {
		fatal(errors.New("-agent or AISCAN_AGENT_ID is required"))
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := aiscan.NewClient(http.DefaultClient, *baseURL, connect.WithProtoJSON())
	sessionID := newID("session")
	opened, err := client.Chat.OpenSession(ctx, authenticated(*token, &aop.OpenSessionRequest{
		RequestId: newID("open"), SessionId: sessionID, Participant: *agentID, Title: "external Go client",
	}))
	if err != nil {
		fatal(err)
	}
	if rejected := opened.Msg.GetRejected(); rejected != nil {
		fatal(fmt.Errorf("OpenSession rejected: %s: %s", rejected.Code, rejected.Message))
	}

	type watchResult struct {
		stream *connect.ServerStreamForClient[aop.WatchEventsResponse]
		err    error
	}
	watchCtx, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	watchReady := make(chan watchResult, 1)
	go func() {
		stream, watchErr := client.Chat.WatchEvents(watchCtx, authenticated(*token, &aop.WatchEventsRequest{SessionId: sessionID}))
		watchReady <- watchResult{stream: stream, err: watchErr}
	}()

	turnID := newID("turn")
	run, err := client.Chat.RunTurn(ctx, authenticated(*token, &aop.RunTurnRequest{
		RequestId: newID("run"), SessionId: sessionID, TurnId: turnID,
		Input: &aop.Message{Id: newID("message"), Role: "user", Name: "external-tool", Content: []*aop.Content{{
			Value: &aop.Content_Text{Text: &aop.TextContent{Text: *prompt}},
		}}},
	}))
	if err != nil {
		fatal(err)
	}
	if rejected := run.Msg.GetRejected(); rejected != nil {
		fatal(fmt.Errorf("RunTurn rejected: %s: %s", rejected.Code, rejected.Message))
	}

	initial := <-watchReady
	if initial.err != nil {
		fatal(initial.err)
	}
	if err := receiveTurn(ctx, client, *token, sessionID, turnID, initial.stream); err != nil {
		fatal(err)
	}
}

func receiveTurn(
	ctx context.Context,
	client *aiscan.Client,
	token, sessionID, turnID string,
	stream *connect.ServerStreamForClient[aop.WatchEventsResponse],
) error {
	var cursor string
	var sawDelta bool
	retry := 250 * time.Millisecond
	for {
		for stream.Receive() {
			delivery := stream.Msg().GetDelivery()
			if delivery == nil || delivery.Event == nil {
				continue
			}
			cursor = delivery.Cursor
			event := delivery.Event
			if event.TurnId != turnID {
				continue
			}
			switch payload := event.Payload.(type) {
			case *aop.Event_MessageDelta:
				if text := payload.MessageDelta.GetText(); text != "" {
					sawDelta = true
					fmt.Print(text)
				}
			case *aop.Event_Message:
				if !sawDelta && payload.Message.GetRole() == "assistant" {
					fmt.Print(messageText(payload.Message))
				}
			case *aop.Event_Error:
				fmt.Fprintf(os.Stderr, "\nprotocol error: %s\n", payload.Error.GetMessage())
			case *aop.Event_TurnEnded:
				ended := payload.TurnEnded
				fmt.Printf("\nstop=%s cursor=%s session=%s turn=%s\n", ended.GetStopReason(), cursor, sessionID, turnID)
				if failure := ended.GetError(); failure != nil {
					return fmt.Errorf("turn failed: %s: %s", failure.Code, failure.Message)
				}
				return nil
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := stream.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "watch disconnected: %v; resuming after cursor %s\n", err, cursor)
		}
		select {
		case <-time.After(retry):
		case <-ctx.Done():
			return ctx.Err()
		}
		retry = min(retry*2, 5*time.Second)
		next, err := client.Chat.WatchEvents(ctx, authenticated(token, &aop.WatchEventsRequest{
			SessionId: sessionID, AfterCursor: cursor,
		}))
		if err != nil {
			continue
		}
		stream = next
	}
}

func authenticated[T any](token string, message *T) *connect.Request[T] {
	request := connect.NewRequest(message)
	if token != "" {
		request.Header().Set("Authorization", "Bearer "+token)
	}
	return request
}

func messageText(message *aop.Message) string {
	var text strings.Builder
	for _, content := range message.GetContent() {
		text.WriteString(content.GetText().GetText())
	}
	return text.String()
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
