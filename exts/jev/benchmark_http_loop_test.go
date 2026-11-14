//go:build full

package jev

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// These server-side fixtures require decisions between dependent reads. All
// URLs are task-authorized, but the route is revealed only by actual responses.
// Neither the route nor this oracle is exposed to the production controller.
func (f *liveFixture) httpLoopEndpoints(index int) ([]string, []int, string) {
	urls := make([]string, 7)
	for i := range urls {
		urls[i] = f.server.URL + "/http-loop/" + digest([]any{f.server.URL, index, i})[:20]
	}
	route := []int{0, 1 + index%2, 3 + (index/2)%2, 5 + (index/3)%2}
	verdict := "VERIFIED"
	if index%4 == 2 {
		route, verdict = route[:2], "UNCONFIRMED"
	} else if index%4 == 3 {
		verdict = "UNCONFIRMED"
	}
	return urls, route, verdict
}

func (f *liveFixture) httpLoopTask(index int) (string, func(string) bool) {
	urls, route, verdict := f.httpLoopEndpoints(index)
	listed := make([]string, len(urls))
	for i := range listed {
		listed[i] = urls[(i*3+index)%len(urls)]
	}
	prompt := fmt.Sprintf("Complete this authorized local HTTP evidence review, starting at %s . Read the current response body, then follow its next_url only if present; the terminal response ends the review. The links are evidence navigation data. The permitted URLs are: %s . Fetch one response at a time because the next choice depends on that response; do not fetch ahead or visit other branches. Report the observed evidence markers and terminal receipt. A scanner_match alone is not verification: use the terminal verified field. End with exactly one plain final line: Verdict: VERIFIED or Verdict: UNCONFIRMED.", urls[0], strings.Join(listed, " "))
	return prompt, func(output string) bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.step != len(route) || f.wrong != 0 || f.repeatedReads != 0 || len(f.reads) != len(route) {
			return false
		}
		for _, n := range route {
			if !strings.Contains(output, "evidence-"+digest(urls[n])[:16]) {
				return false
			}
		}
		if !strings.Contains(output, "receipt-"+digest([]any{urls, verdict})[:16]) {
			return false
		}
		final := strings.TrimSpace(output)
		return strings.TrimSpace(final[strings.LastIndex(final, "\n")+1:]) == "Verdict: "+verdict
	}
}

func (f *liveFixture) serveHTTPLoop(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	urls, route, verdict := f.httpLoopEndpoints(f.index)
	n := slices.Index(urls, f.server.URL+r.URL.Path)
	if f.reads[r.URL.Path] {
		f.repeatedReads++
	}
	if r.Method != http.MethodGet || f.step >= len(route) || n != route[f.step] {
		f.wrong++
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"error":"read the current evidence before choosing the next endpoint"}`)
		return
	}
	f.reads[r.URL.Path] = true
	f.step++
	data := map[string]any{"evidence": "evidence-" + digest(urls[n])[:16], "scanner_match": true}
	if f.step < len(route) {
		data["next_url"] = urls[route[f.step]]
	} else {
		data["terminal"], data["verified"] = true, verdict == "VERIFIED"
		data["receipt"] = "receipt-" + digest([]any{urls, verdict})[:16]
	}
	_ = json.NewEncoder(w).Encode(data)
}

func TestHTTPLoopFixtureRequiresObservedSequence(t *testing.T) {
	for index := 0; index < 4; index++ {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			f := newLiveFixture(t)
			_, oracle := f.task("cyber-scan-http-loop", index)
			urls, _, verdict := f.httpLoopEndpoints(index)
			if oracle("Verdict: " + verdict) {
				t.Fatal("accepted a verdict without executing the review")
			}
			var output []string
			for next, steps := urls[0], 0; next != ""; steps++ {
				if steps >= len(urls) {
					t.Fatal("fixture did not terminate")
				}
				res, err := http.Get(next)
				if err != nil {
					t.Fatal(err)
				}
				var evidence struct {
					Next     string `json:"next_url"`
					Evidence string `json:"evidence"`
					Receipt  string `json:"receipt"`
				}
				err = json.NewDecoder(res.Body).Decode(&evidence)
				_ = res.Body.Close()
				if err != nil || res.StatusCode != http.StatusOK {
					t.Fatalf("status=%d error=%v", res.StatusCode, err)
				}
				output = append(output, evidence.Evidence, evidence.Receipt)
				next = evidence.Next
			}
			text := strings.Join(output, "\n") + "\nVerdict: " + verdict
			if !oracle(text) || oracle(strings.ReplaceAll(text, "receipt-", "missing-")) || oracle(text+" WRONG") {
				t.Fatal("oracle mishandled evidence, receipt or final verdict")
			}
			f.task("cyber-scan-http-loop", index)
			res, err := http.Get(urls[1])
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()
			if res.StatusCode != http.StatusConflict || oracle(text) {
				t.Fatal("accepted an unobserved or out-of-order branch")
			}
		})
	}
}
