package passive_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestFofaCommandCancellationIsNotEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	engine := passive.NewUncoverEngine(passive.ReconOptions{FofaKey: "local-secret", FofaURL: server.URL}, nil)
	command := passive.New(engine)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	_, err := command.Run(ctx, &coretool.Execution{Args: []string{"-s", "fofa", "anything"}, Stdout: &output, Stderr: &output})
	if !errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 {
		t.Fatalf("cancelled query returned empty success: output=%s err=%v", output.String(), err)
	}
}
