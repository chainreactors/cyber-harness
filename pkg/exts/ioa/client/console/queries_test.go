package console

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	clientext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
)

func TestConsoleQueriesReuseExtensionIdentity(t *testing.T) {
	var registrations, queries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/register" {
			registrations.Add(1)
			fmt.Fprint(w, `{"id":"shared-node","token":"shared-token"}`)
			return
		}
		queries.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer shared-token" {
			http.Error(w, "query must use extension identity", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/spaces":
			fmt.Fprint(w, `[{"id":"space","name":"team"}]`)
		case "/spaces/team":
			fmt.Fprint(w, `{"id":"space","name":"team"}`)
		case "/nodes":
			fmt.Fprint(w, `[{"id":"shared-node","name":"peer"}]`)
		case "/spaces/space/messages":
			fmt.Fprint(w, `[{"id":"message","sender":"peer","content":{"text":"hello"}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := clientext.New(ioatools.Config{
		URL: strings.Replace(server.URL, "http://", "http://key@", 1), AutoRegister: true,
	}, clientext.Services{})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(extension.Entry{ID: "client", Extension: client})
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	bindings := Bind(client.Runtime(), "", "")
	view := consoleapi.View{Out: &output, Err: &output, Table: func(title string, rows [][]string) { fmt.Fprintln(&output, title, rows) }}
	arguments := map[string][]string{
		"/spaces": {}, "/nodes": {}, "/messages": {"team"}, "/context": {"team", "message"},
	}
	for _, command := range bindings.Commands(view) {
		command.SetArgs(arguments[command.Name()])
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatalf("%s: %v", command.Name(), err)
		}
	}
	bindings.Complete(t.Context(), "")
	if registrations.Load() != 1 || queries.Load() != 7 {
		t.Fatalf("requests: registrations=%d queries=%d", registrations.Load(), queries.Load())
	}
	if !strings.Contains(output.String(), "hello") || !strings.Contains(output.String(), "peer") {
		t.Fatalf("missing query output: %s", output.String())
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	command := bindings.Commands(view)[0]
	command.SetArgs([]string{})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("console query succeeded after extension Close")
	}
	if registrations.Load() != 1 || queries.Load() != 7 {
		t.Fatal("console recreated a client after extension Close")
	}
}
