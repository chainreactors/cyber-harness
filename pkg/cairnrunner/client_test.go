package cairnrunner

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/gorilla/websocket"
)

func TestNativeRunnerExecAndFiles(t *testing.T) {
	workDir := t.TempDir()
	registry := commands.NewRegistry()
	commands.BuildGroup("core", &commands.Deps{WorkDir: workDir, Logger: telemetry.NopLogger()}, registry)
	defer func() {
		for _, tool := range registry.Tools() {
			if closer, ok := tool.(interface{ Close() }); ok {
				closer.Close()
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverDone := make(chan error, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	writtenPath := filepath.Join(workDir, "written.txt")
	readPath := filepath.Join(workDir, "read.txt")
	if err := os.WriteFile(readPath, []byte("read-content"), 0o644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		var hello message
		if err := conn.ReadJSON(&hello); err != nil {
			serverDone <- err
			return
		}
		if hello.T != "hello" || hello.RunnerID != "runner-test" || r.URL.Query().Get("token") != "secret" {
			serverDone <- &testError{"unexpected hello"}
			return
		}
		if err := conn.WriteJSON(message{T: "welcome"}); err != nil {
			serverDone <- err
			return
		}

		execRaw, _ := json.Marshal(execParams{Command: "echo native", Cwd: workDir, Timeout: 5})
		if err := conn.WriteJSON(message{T: "req", ID: 1, Method: "exec", Params: execRaw}); err != nil {
			serverDone <- err
			return
		}
		var output strings.Builder
		for {
			var response message
			if err := conn.ReadJSON(&response); err != nil {
				serverDone <- err
				return
			}
			if response.T == "exec_out" {
				data, _ := base64.StdEncoding.DecodeString(response.Data)
				output.Write(data)
				continue
			}
			if response.T == "res" && response.ID == 1 {
				if !response.OK || !strings.Contains(output.String(), "native") {
					serverDone <- &testError{"exec failed"}
					return
				}
				break
			}
		}

		writeRaw, _ := json.Marshal(fileParams{Path: writtenPath, Size: 7})
		_ = conn.WriteJSON(message{T: "req", ID: 2, Method: "write_file", Params: writeRaw})
		frame := make([]byte, 11)
		binary.BigEndian.PutUint32(frame[:4], 2)
		copy(frame[4:], []byte("written"))
		_ = conn.WriteMessage(websocket.BinaryMessage, frame)
		_ = conn.WriteJSON(message{T: "write_end", ID: 2})
		var writeResult message
		if err := conn.ReadJSON(&writeResult); err != nil || !writeResult.OK {
			serverDone <- err
			return
		}

		readRaw, _ := json.Marshal(fileParams{Path: readPath})
		_ = conn.WriteJSON(message{T: "req", ID: 3, Method: "read_file", Params: readRaw})
		var readData []byte
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				serverDone <- err
				return
			}
			if kind == websocket.BinaryMessage {
				if binary.BigEndian.Uint32(data[:4]) == 3 {
					readData = append(readData, data[4:]...)
				}
				continue
			}
			var response message
			_ = json.Unmarshal(data, &response)
			if response.T == "res" && response.ID == 3 {
				if !response.OK || string(readData) != "read-content" {
					serverDone <- &testError{"read failed"}
					return
				}
				break
			}
		}
		serverDone <- nil
	}))
	defer server.Close()

	client, err := New(Config{
		ServerURL: server.URL,
		Token:     "secret",
		RunnerID:  "runner-test",
		Registry:  registry,
		Logger:    telemetry.NopLogger(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = client.Run(ctx) }()
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runner protocol test timed out")
	}
	cancel()
	data, err := os.ReadFile(writtenPath)
	if err != nil || string(data) != "written" {
		t.Fatalf("written file = %q, err=%v", data, err)
	}
}

type testError struct{ message string }

func (e *testError) Error() string { return e.message }
