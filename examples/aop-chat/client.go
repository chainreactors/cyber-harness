package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "AIScan gRPC address")
	token := flag.String("token", os.Getenv("AISCAN_WEB_TOKEN"), "AIScan access token")
	agentID := flag.String("agent", "", "connected Agent ID")
	prompt := flag.String("prompt", "你好", "natural-language prompt")
	flag.Parse()
	if *agentID == "" {
		fmt.Fprintln(os.Stderr, "-agent is required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if *token != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+*token)
	}
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fatal(err)
	}
	defer conn.Close()
	client := aop.NewChatServiceClient(conn)
	sessionID := fmt.Sprintf("example-%d", time.Now().UnixNano())
	opened, err := client.OpenSession(ctx, &aop.OpenSessionRequest{RequestId: "open:" + sessionID, SessionId: sessionID, Participant: *agentID})
	if err != nil {
		fatal(err)
	}
	if rejected := opened.GetRejected(); rejected != nil {
		fatal(fmt.Errorf("open rejected: %s: %s", rejected.Code, rejected.Message))
	}
	watch, err := client.WatchEvents(ctx, &aop.WatchEventsRequest{SessionId: sessionID})
	if err != nil {
		fatal(err)
	}
	turnID := "turn:" + sessionID
	run, err := client.RunTurn(ctx, &aop.RunTurnRequest{
		RequestId: turnID, SessionId: sessionID, TurnId: turnID,
		Input: &aop.Message{Id: "input:" + sessionID, Role: "user", Content: []*aop.Content{{Value: &aop.Content_Text{Text: &aop.TextContent{Text: *prompt}}}}},
	})
	if err != nil {
		fatal(err)
	}
	if rejected := run.GetRejected(); rejected != nil {
		fatal(fmt.Errorf("run rejected: %s: %s", rejected.Code, rejected.Message))
	}
	for {
		response, err := watch.Recv()
		if err == io.EOF {
			return
		}
		if err != nil {
			fatal(err)
		}
		event := response.GetDelivery().GetEvent()
		switch payload := event.Payload.(type) {
		case *aop.Event_MessageDelta:
			fmt.Print(payload.MessageDelta.GetText())
		case *aop.Event_Message:
			fmt.Println()
			for _, content := range payload.Message.Content {
				fmt.Print(content.GetText().GetText())
			}
			fmt.Println()
		case *aop.Event_TurnEnded:
			if event.TurnId == turnID {
				if payload.TurnEnded.Error != nil {
					fatal(fmt.Errorf("turn failed: %s: %s", payload.TurnEnded.Error.Code, payload.TurnEnded.Error.Message))
				}
				return
			}
		case *aop.Event_Error:
			fmt.Fprintln(os.Stderr, payload.Error.Message)
		}
	}
}

func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
