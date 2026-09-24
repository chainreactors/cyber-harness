package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/audit/internal/toolchain"
	"github.com/chainreactors/cyber/core/telemetry"
	"google.golang.org/protobuf/encoding/protojson"
)

func captureStdout(t *testing.T, run func() error) (string, error) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = writer
	defer func() { os.Stdout = original; writer.Close(); reader.Close() }()
	output := make(chan []byte, 1)
	go func() { body, _ := io.ReadAll(reader); output <- body }()
	err = run()
	writer.Close()
	os.Stdout = original
	return string(<-output), err
}

func TestAuditWorkflowSurvivesProjectSkillCollision(t *testing.T) {
	workspace := t.TempDir()
	const marker = "PROJECT_AUDIT_SUPPLEMENT"
	for _, name := range []string{"audit", "local"} {
		dir := filepath.Join(workspace, ".agent", "skills", name)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: Project supplement\n---\n"+marker), 0600); err != nil {
			t.Fatal(err)
		}
	}
	requests := make(chan string, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "cyber-audit") {
			textReply(w, "pong")
			return
		}
		requests <- string(body)
		streamReply(w, "done")
	}))
	defer server.Close()
	option := testOption(t, server.URL)
	report, err := newReport(t.Context(), workspace, "", "review", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := newAuditProfile(option, telemetry.NopLogger(), workspace, 10, report)
	if err != nil {
		t.Fatal(err)
	}
	if err := profile.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer profile.Close(context.Background())
	for _, name := range []string{"audit", "local"} {
		if _, ok := profile.runtime.Skills().ByName(name); !ok {
			t.Fatalf("project skill %s was not discovered", name)
		}
	}
	for _, selectSkill := range []bool{false, true} {
		task := "Review the repository"
		if selectSkill {
			task, err = profile.runtime.Skills().ApplySelected(task, []string{"audit"})
			if err != nil {
				t.Fatal(err)
			}
		}
		session, err := profile.runtime.OpenSession(t.Context(), agentsession.SessionOptions{})
		if err != nil {
			t.Fatal(err)
		}
		run, err := session.Run(t.Context(), agentsession.RunInput{Content: []*aop.Content{aop.Text(task)}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.Wait(); err != nil {
			t.Fatal(err)
		}
		body := <-requests
		if strings.Count(body, "# Audit workflow") != 1 || !strings.Contains(body, "coverage.json") {
			t.Fatal("built-in workflow was replaced or duplicated")
		}
		if strings.Contains(body, marker) != selectSkill {
			t.Fatal("project skill was not selected explicitly")
		}
	}
}

func TestAuditConfigurationCommandsUseSameLLMPolicy(t *testing.T) {
	workspace := t.TempDir()
	config := "llm:\n  active_profile: target-only-profile\n  base_url: https://target.invalid/v1\nagent:\n  timeout: 19\n"
	if err := os.WriteFile(filepath.Join(workspace, "cyber.yaml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	var output, diagnostics strings.Builder
	err := Run(t.Context(), []string{"config", "show", "--workdir", workspace, "--json"}, &output, &diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(output.String())) || strings.Contains(output.String(), "target.invalid") || !strings.Contains(diagnostics.String(), "ignoring llm") {
		t.Fatal("config show did not enforce the audit configuration policy")
	}
	_, option, err := parseOptions([]string{"--workdir", workspace}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !option.Context.UserLLMOnly || option.Context.Directory != workspace {
		t.Fatal("run/tool command configuration policy differs")
	}
}

func TestAuditFinalOutputIncludesReportOutcome(t *testing.T) {
	for _, format := range []string{"json", "stream-json"} {
		for _, outcome := range []string{"valid", "missing", "invalid-okf", "write-failure", "model-failure", "canceled"} {
			t.Run(format+"/"+outcome, func(t *testing.T) {
				workspace := t.TempDir()
				reportDir := filepath.Join(workspace, "report")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, _ := io.ReadAll(r.Body)
					if !strings.Contains(string(body), "cyber-audit") {
						textReply(w, "pong")
						return
					}
					if outcome == "model-failure" {
						http.Error(w, "fixture provider failure", http.StatusBadRequest)
						return
					}
					if outcome == "canceled" {
						cancel()
						return
					}
					if outcome != "missing" {
						coverage := `{"reviewed":true,"scope":"fixture","examined":["fixture.go"],"excluded":[],"unsupported":[],"unresolved":[],"checks":[{"tool":"osv-scanner","status":"not_applicable","evidence":"No manifests"},{"tool":"proton","status":"completed","evidence":"raw/proton.jsonl"}]}`
						index := "---\nokf_version: '0.2'\n---\n# Audit results\n"
						if outcome == "invalid-okf" {
							index = "---\nokf_version: '0.2'\nextra: invalid\n---\n# Audit results\n"
						}
						for name, content := range map[string]string{"coverage.json": coverage, "index.md": index, "raw/proton.jsonl": ""} {
							if err := os.WriteFile(filepath.Join(reportDir, name), []byte(content), 0600); err != nil {
								t.Error(err)
								http.Error(w, "fixture write failed", 500)
								return
							}
						}
						if outcome == "write-failure" {
							path := filepath.Join(reportDir, "run.json")
							if err := os.Remove(path); err != nil {
								t.Error(err)
								return
							}
							if err := os.Mkdir(path, 0700); err != nil {
								t.Error(err)
								return
							}
						}
					}
					streamReply(w, "Model finished.")
				}))
				defer server.Close()
				output, runErr := captureStdout(t, func() error {
					return run(ctx, []string{"--workdir", workspace, "--data-dir", t.TempDir(), "--report-dir", reportDir, "--provider", "openai", "--base-url", server.URL, "--api-key", "fixture", "--model", "fixture", "-p", "Review", "--output-format", format, "--quiet", "--no-color"}, io.Discard, io.Discard, fakeTools)
				})
				wantFailure := outcome != "valid"
				if (runErr != nil) != wantFailure {
					t.Fatalf("run outcome: %v", runErr)
				}
				if format == "json" {
					var result struct {
						IsError bool   `json:"is_error"`
						Stop    string `json:"stop_reason"`
					}
					if err := json.Unmarshal([]byte(output), &result); err != nil {
						t.Fatalf("result JSON: %v", err)
					}
					wantStop := "error"
					if outcome == "canceled" {
						wantStop = "canceled"
					}
					if result.IsError != wantFailure || wantFailure && result.Stop != wantStop {
						t.Fatalf("result: %+v", result)
					}
				} else {
					lines := strings.Split(strings.TrimSpace(output), "\n")
					last := new(aop.Event)
					if err := protojson.Unmarshal([]byte(lines[len(lines)-1]), last); err != nil {
						t.Fatal(err)
					}
					if (last.GetError() != nil) != wantFailure || last.Id == "" || last.Seq == 0 {
						t.Fatalf("unexpected final event: %s", lines[len(lines)-1])
					}
				}
				if outcome != "write-failure" {
					body, err := os.ReadFile(filepath.Join(reportDir, "run.json"))
					if err != nil {
						t.Fatal(err)
					}
					var report runReport
					if err := json.Unmarshal(body, &report); err != nil {
						t.Fatal(err)
					}
					want := "completed"
					if wantFailure {
						want = "incomplete"
					}
					if outcome == "canceled" {
						want = "interrupted"
					}
					if outcome == "model-failure" {
						want = "failed"
					}
					if report.Status != want {
						t.Fatalf("report status: %s", report.Status)
					}
				}
			})
		}
	}
}

func TestAuditTimeoutIncludesPreparationAndProviderStartup(t *testing.T) {
	for _, stage := range []string{"tools", "provider"} {
		t.Run(stage, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				select {
				case <-r.Context().Done():
				case <-time.After(4 * time.Second):
				}
			}))
			defer server.Close()
			ensure := fakeTools
			if stage == "tools" {
				ensure = func(ctx context.Context, _ string, _ io.Writer) ([]toolchain.Status, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
			defer cancel()
			started := time.Now()
			err := run(ctx, []string{"--workdir", t.TempDir(), "--data-dir", t.TempDir(), "--provider", "openai", "--base-url", server.URL, "--api-key", "fixture", "--model", "fixture", "--timeout", "1", "-p", "Review"}, io.Discard, io.Discard, ensure)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout: %v", err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("overall timeout did not include startup")
			}
		})
	}
}
