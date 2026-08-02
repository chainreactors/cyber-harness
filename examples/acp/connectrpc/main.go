package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	aop "github.com/chainreactors/aiscan/aop"
	rpc "github.com/chainreactors/aiscan/pkg/rpc"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
)

// connectrpc example: query aiscan's management plane. Unlike the Application
// WebSocket client, this program performs finite unary queries and does not
// subscribe to live agent events.
func main() {
	var (
		serverURL string
		token     string
		sessionID string
		limit     uint
	)
	flag.StringVar(&serverURL, "server", "http://127.0.0.1:8080", "aiscan server base URL")
	flag.StringVar(&token, "token", "", "server access token")
	flag.StringVar(&sessionID, "session", "", "session ID; when set, list its persisted events")
	flag.UintVar(&limit, "limit", 100, "maximum number of sessions or events")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := run(ctx, os.Stdout, http.DefaultClient, serverURL, token, sessionID, uint32(limit)); err != nil {
		fmt.Fprintf(os.Stderr, "connectrpc: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer, httpClient connect.HTTPClient, serverURL, token, sessionID string, limit uint32) error {
	client := rpc.NewSessionServiceClient(httpClient, strings.TrimRight(serverURL, "/"))
	if strings.TrimSpace(sessionID) == "" {
		request := connect.NewRequest(&types.ListSessionsRequest{Limit: limit, IncludeClosed: true})
		setBearer(request.Header(), token)
		response, err := client.ListSessions(ctx, request)
		if err != nil {
			return err
		}
		return writeProtoJSON(out, response.Msg)
	}

	request := connect.NewRequest(&aop.ListEventsRequest{SessionId: sessionID, Limit: limit})
	setBearer(request.Header(), token)
	response, err := client.ListEvents(ctx, request)
	if err != nil {
		return err
	}
	return writeProtoJSON(out, response.Msg)
}

func setBearer(header http.Header, token string) {
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
}

func writeProtoJSON(out io.Writer, message protobuf.Message) error {
	data, err := (protojson.MarshalOptions{Indent: "  ", UseProtoNames: true}).Marshal(message)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
