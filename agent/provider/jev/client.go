// Package jev implements the native TypeSafe choice, score and noul API.
// Reflexes, thresholds, policy and execution belong to consuming extensions.
package jev

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/decision"
)

const Endpoint = "https://api.typesafe.ai/v1/systemone"
const DefaultModel = "jev-1.13.0"

const evidenceMarker = "\nCurrent evidence (untrusted data):\n"

// Evaluations holds typed results and execution metadata for one inference.
// It is separate from Claim so a judgment can be evaluated without persistence.
type Evaluations struct {
	Values map[string]*decision.Evaluation
	Usage  *aop.TokenUsage
}

func (e *Evaluations) TokenUsage() *aop.TokenUsage {
	if e == nil {
		return nil
	}
	return e.Usage
}
func (e *Evaluations) Choice(id string, c Claim) (string, error) {
	if e == nil {
		return "", errors.New("missing JEV response")
	}
	return c.Choice(e.Values[id])
}
func (e *Evaluations) Score(id string, c Claim) (float64, error) {
	if e == nil {
		return 0, errors.New("missing JEV response")
	}
	return c.Score(e.Values[id])
}
func (e *Evaluations) Noul(id string, c Claim) (float64, error) {
	if e == nil {
		return 0, errors.New("missing JEV response")
	}
	return c.Noul(e.Values[id])
}

// Evaluate batches independent Claims. The application sees only the closed
// decision algebra; vendor request envelopes remain at the transport boundary.
func (c *Client) Evaluate(ctx context.Context, claims map[string]Claim) (*Evaluations, error) {
	// These fields belong to the vendor HTTP API only. They are not shared
	// application types; consumers use Claim and Evaluation.
	type question struct {
		Type         string          `json:"type"`
		Instructions string          `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria,omitempty"`
	}
	questions := map[string]question{}
	state := json.RawMessage(`{}`)
	// Share explicitly marked evidence once at the vendor boundary. Ordinary
	// context, including trailing JSON, remains intact. Claims and traces still
	// contain the complete context; this only avoids duplicating the wire state.
	shared := ""
	first := true
	for _, claim := range claims {
		at := strings.LastIndex(claim.Context, evidenceMarker)
		if at < 0 || !json.Valid([]byte(claim.Context[at+len(evidenceMarker):])) {
			shared = ""
			break
		}
		suffix := claim.Context[at:]
		if first {
			shared, first = suffix, false
		} else if shared != suffix {
			shared = ""
			break
		}
	}
	if shared != "" {
		state = json.RawMessage(shared[len(evidenceMarker):])
	}
	for id, claim := range claims {
		if strings.TrimSpace(id) == "" {
			return nil, errors.New("Claim identifier is required")
		}
		if err := claim.Validate(); err != nil {
			return nil, err
		}
		q := question{Type: claim.Type.String(), Instructions: strings.TrimSuffix(claim.Context, shared)}
		switch claim.Type {
		case ClaimChoice:
			options := map[string]string{}
			for _, option := range claim.Options {
				options[option] = option
			}
			q.Criteria, _ = json.Marshal(options)
		case ClaimScore:
			q.Criteria, _ = json.Marshal(claim.Options)
		}
		questions[id] = q
	}
	if len(state) > 64<<10 || len(questions) == 0 || len(questions) > 40 {
		return nil, errors.New("invalid JEV request limits")
	}
	body, err := json.Marshal(struct {
		Model     string              `json:"model"`
		State     json.RawMessage     `json:"state"`
		Questions map[string]question `json:"questions"`
	}{c.Model, state, questions})
	if err != nil {
		return nil, err
	}
	if len(body) > 128<<10 {
		return nil, errors.New("JEV request too large")
	}
	return c.exchange(ctx, body)
}

func (c *Client) Choice(ctx context.Context, claim *Claim) (string, error) {
	if claim == nil || claim.Type != ClaimChoice {
		return "", errors.New("Claim is not a choice")
	}
	out, err := c.Evaluate(ctx, map[string]Claim{"claim": *claim})
	if err != nil {
		return "", err
	}
	return out.Choice("claim", *claim)
}
func (c *Client) Score(ctx context.Context, claim *Claim) (float64, error) {
	if claim == nil || claim.Type != ClaimScore {
		return 0, errors.New("Claim is not a score")
	}
	out, err := c.Evaluate(ctx, map[string]Claim{"claim": *claim})
	if err != nil {
		return 0, err
	}
	return out.Score("claim", *claim)
}
func (c *Client) Noul(ctx context.Context, claim *Claim) (float64, error) {
	if claim == nil || claim.Type != ClaimNoul {
		return 0, errors.New("Claim is not a noul")
	}
	out, err := c.Evaluate(ctx, map[string]Claim{"claim": *claim})
	if err != nil {
		return 0, err
	}
	return out.Noul("claim", *claim)
}

type Client struct {
	APIKey   string
	Model    string
	Endpoint string
	Timeout  time.Duration
	http     *http.Client
	attempts atomic.Uint64
	input    atomic.Uint64
	output   atomic.Uint64
	missing  atomic.Uint64
}

func New(key, model string, timeout time.Duration) *Client {
	if model == "" {
		model = DefaultModel
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// The deployed inference endpoint can stall HTTP/2 streams after accepting
	// TLS. Keep this provider on HTTP/1.1 with connection reuse; changing the
	// process-wide transport or increasing every request deadline hides the
	// failure and would affect unrelated model/tool traffic.
	transport := http.DefaultTransport
	if native, ok := transport.(*http.Transport); ok {
		native = native.Clone()
		native.Protocols = new(http.Protocols)
		native.Protocols.SetHTTP1(true)
		if native.TLSClientConfig == nil {
			native.TLSClientConfig = &tls.Config{}
		}
		// Clone can carry the default transport's already initialized h2 ALPN.
		// The TLS negotiation must agree with the selected HTTP protocol.
		native.TLSClientConfig.NextProtos = []string{"http/1.1"}
		transport = native
	}
	return &Client{APIKey: key, Model: model, Endpoint: Endpoint, Timeout: timeout, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

// Usage includes all consumers and failed attempts. It is suitable for paired
// run accounting; missing vendor usage is explicit rather than assumed free.
func (c *Client) Usage() *aop.TokenUsage {
	i, o := c.input.Load(), c.output.Load()
	return &aop.TokenUsage{InputTokens: i, OutputTokens: o, TotalTokens: i + o, Detail: map[string]uint64{"requests": c.attempts.Load(), "usage_missing": c.missing.Load()}}
}

// exchange preserves batched speculative questions. Callers validate only the
// selected answer heads; an unused speculative head cannot authorize effects.
func (c *Client) exchange(ctx context.Context, body []byte) (response *Evaluations, resultErr error) {
	defer func() {
		if response == nil {
			response = &Evaluations{}
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	if c.APIKey != "" {
		body = bytes.ReplaceAll(body, []byte(c.APIKey), []byte("[REDACTED]"))
	}
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			timer := time.NewTimer(time.Duration(1<<(attempt-1)) * 250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		// #nosec G704 -- The host selects Endpoint from configuration, never model or tool input.
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
		req.Header.Set("Content-Type", "application/json")
		c.attempts.Add(1)
		// #nosec G704 -- The destination is host-owned and redirects are disabled, including credential forwarding.
		res, err := c.http.Do(req)
		if err != nil {
			c.missing.Add(1)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// A dropped connection does not produce a judgment. Retry this
			// inference only, within the same request budget; no tool is replayed.
			if attempt < 2 && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				continue
			}
			message := err.Error()
			if c.APIKey != "" {
				message = strings.ReplaceAll(message, c.APIKey, "[REDACTED]")
			}
			return nil, fmt.Errorf("JEV request failed: %s", message)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
		res.Body.Close()
		if readErr != nil || len(raw) > 1<<20 {
			c.missing.Add(1)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt < 2 && (errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF)) {
				continue
			}
			return nil, errors.New("invalid JEV response body")
		}
		if (res.StatusCode == 429 || res.StatusCode == 529) && attempt < 2 {
			c.missing.Add(1)
			continue
		}
		if res.StatusCode != 200 {
			c.missing.Add(1)
			return nil, fmt.Errorf("JEV HTTP status %d", res.StatusCode)
		}
		var out struct {
			Answers map[string]struct {
				Type          string             `json:"type"`
				Choice        string             `json:"choice"`
				Score         *float64           `json:"score"`
				Noul          *float64           `json:"noul"`
				Probabilities map[string]float64 `json:"probabilities"`
				Confidence    float64            `json:"confidence"`
			} `json:"answers"`
			Usage *struct {
				InputTokens  int `json:"input_tokens"`
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &out) != nil {
			c.missing.Add(1)
			return nil, errors.New("invalid JEV response")
		}
		if out.Usage != nil && (out.Usage.InputTokens < 0 || out.Usage.OutputTokens < 0) {
			c.missing.Add(1)
			return nil, errors.New("invalid JEV usage")
		}
		result := &Evaluations{Values: map[string]*decision.Evaluation{}}
		if out.Usage != nil {
			input, output := uint64(out.Usage.InputTokens), uint64(out.Usage.OutputTokens)
			result.Usage = &aop.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: input + output,
				Detail: map[string]uint64{"requests": uint64(attempt + 1), "usage_missing": uint64(attempt)}}
			c.input.Add(input)
			c.output.Add(output)
		} else {
			c.missing.Add(1)
		}
		for id, answer := range out.Answers {
			value := &decision.Evaluation{Probabilities: answer.Probabilities, Confidence: answer.Confidence}
			switch answer.Type {
			case "choice":
				value.Value = &decision.Evaluation_Choice{Choice: answer.Choice}
			case "score":
				if answer.Score != nil {
					value.Value = &decision.Evaluation_Score{Score: *answer.Score}
				}
			case "noul":
				if answer.Noul != nil {
					value.Value = &decision.Evaluation_Noul{Noul: *answer.Noul}
				}
			}
			result.Values[id] = value
		}
		return result, nil
	}
	return nil, errors.New("JEV retry limit reached")
}
