package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/aop"
)

func TestNonMultimodalModelRecoversAndContinues(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "completion", true: "stream"}[stream], func(t *testing.T) {
			var calls, imageRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if strings.Contains(string(request), "image_url") {
					imageRequests.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"message":"dsv4s is not a multimodal model","type":"BadRequestError","param":null,"code":400}}`))
					return
				}
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			images := true
			llm, err := provider.NewOpenAIProvider(&provider.ProviderConfig{BaseURL: server.URL, Model: "dsv4s", Images: &images})
			if err != nil {
				t.Fatal(err)
			}
			a := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: "dsv4s", Stream: stream})
			a.LoadMessages([]*aop.Message{{Role: "user", Content: []*aop.Content{aop.Text("inspect screenshot"), aop.Image("image/png", []byte("image"))}}})
			for _, prompt := range []string{"continue", "follow up"} {
				result, err := a.Run(t.Context(), TextInput(prompt))
				if err != nil {
					t.Fatal(err)
				}
				if result.Output != "recovered" {
					t.Fatalf("output = %q", result.Output)
				}
			}
			if calls.Load() != 3 || imageRequests.Load() != 1 {
				t.Fatalf("calls=%d image requests=%d; want one rejected request, one retry, one follow-up", calls.Load(), imageRequests.Load())
			}
		})
	}
}
