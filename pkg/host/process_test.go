package host_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/host"
	"google.golang.org/protobuf/proto"
)

// Use a real child process to verify framing and shutdown without a model,
// tools, product configuration, or external network dependencies.
func TestStdioChildProcess(t *testing.T) {
	const helper = "CYBER_HARNESS_HOST_TEST_CHILD"
	if os.Getenv(helper) == "1" {
		mux := aop.NewNamespaceMux()
		err := mux.Register(&aop.ProtocolMessage{}, func(_ context.Context, request *aop.Envelope, message proto.Message, send aop.SendFunc) error {
			return send(aop.Reply(request.Id, message))
		})
		if err == nil {
			h := host.New(context.Background(), mux)
			err = h.Serve(host.NewStdio(os.Stdin, os.Stdout))
			h.Close()
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestStdioChildProcess$")
	cmd.Env = append(os.Environ(), helper+"=1")
	var input, output, stderr bytes.Buffer
	writer := host.NewStdio(bytes.NewReader(nil), &input)
	for _, id := range []string{"first", "second"} {
		if err := writer.Send(aop.MustWrap(id, "", aop.NewProtocolError("ECHO", id))); err != nil {
			t.Fatal(err)
		}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = &input, &output, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("child: %v, stderr: %s", err, stderr.String())
	}
	reader := host.NewStdio(&output, io.Discard)
	for _, id := range []string{"first", "second"} {
		response, err := reader.Recv()
		if err != nil {
			t.Fatal(err)
		}
		message, err := aop.Unwrap(response)
		if err != nil || response.ReplyTo != id || !proto.Equal(message, aop.NewProtocolError("ECHO", id)) {
			t.Fatalf("reply=%v err=%v", response, err)
		}
	}
	if _, err := reader.Recv(); err != io.EOF {
		t.Fatalf("unexpected stdout after replies: %v", err)
	}
}
