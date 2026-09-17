package aopws

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/gorilla/websocket"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func TestStreamRoundTripsBothEncodings(t *testing.T) {
	for _, test := range []struct {
		name     string
		encoding Encoding
		frame    int
	}{
		{name: "binary", encoding: Binary, frame: websocket.BinaryMessage},
		{name: "protojson", encoding: ProtoJSON, frame: websocket.TextMessage},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := make(chan int, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := testUpgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer conn.Close()
				frame, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				frames <- frame
				_ = conn.WriteMessage(frame, data)
			}))
			defer server.Close()

			conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
			if err != nil {
				t.Fatal(err)
			}
			stream, err := New(t.Context(), conn, Options{Encoding: test.encoding, WriteTimeout: time.Second})
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			if err := stream.Send(&aop.Envelope{Id: "round-trip"}); err != nil {
				t.Fatal(err)
			}
			received, err := stream.Recv()
			if err != nil {
				t.Fatal(err)
			}
			if received.GetId() != "round-trip" {
				t.Fatalf("received id = %q", received.GetId())
			}
			if frame := <-frames; frame != test.frame {
				t.Fatalf("frame = %d, want %d", frame, test.frame)
			}
		})
	}
}

func TestStreamSerializesConcurrentWrites(t *testing.T) {
	const writes = 32
	serverResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			serverResult <- err
			return
		}
		defer conn.Close()
		for range writes {
			if _, _, err := conn.ReadMessage(); err != nil {
				serverResult <- err
				return
			}
		}
		serverResult <- nil
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := New(context.Background(), conn, Options{WriteTimeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	var wg sync.WaitGroup
	errs := make(chan error, writes)
	for i := range writes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- stream.Send(&aop.Envelope{Id: fmt.Sprintf("write-%d", i)})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := <-serverResult; err != nil {
		t.Fatal(err)
	}
}

func TestStreamReportsConsistentDecodeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteMessage(websocket.BinaryMessage, []byte{0xff})
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial(strings.Replace(server.URL, "http", "ws", 1), nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := New(t.Context(), conn, Options{Encoding: Binary})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if _, err := stream.Recv(); err == nil || !strings.Contains(err.Error(), "decode AOP envelope") {
		t.Fatalf("Recv() error = %v", err)
	}
}
