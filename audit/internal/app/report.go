package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainreactors/cyber/audit/internal/toolchain"
	"github.com/chainreactors/cyber/tools/okf"
)

type runReport struct {
	Directory string             `json:"-"`
	ID        string             `json:"id"`
	Workspace string             `json:"workspace"`
	Goal      string             `json:"goal,omitempty"`
	Revision  string             `json:"revision,omitempty"`
	Dirty     *bool              `json:"dirty,omitempty"`
	GitError  string             `json:"git_error,omitempty"`
	Resume    string             `json:"resume,omitempty"`
	Started   time.Time          `json:"started"`
	Finished  *time.Time         `json:"finished,omitempty"`
	Status    string             `json:"status"`
	Error     string             `json:"error,omitempty"`
	Tools     []toolchain.Status `json:"tools"`
}
type coverage struct {
	Reviewed    bool          `json:"reviewed"`
	Scope       string        `json:"scope"`
	Examined    []string      `json:"examined"`
	Excluded    []string      `json:"excluded"`
	Unsupported []string      `json:"unsupported"`
	Unresolved  []string      `json:"unresolved"`
	Checks      []checkRecord `json:"checks"`
}
type checkRecord struct {
	Tool     string `json:"tool"`
	Status   string `json:"status"`
	Evidence string `json:"evidence"`
}
type finding struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Status         string   `json:"status"`
	Severity       string   `json:"severity"`
	Location       string   `json:"location"`
	Preconditions  string   `json:"preconditions"`
	Trace          []string `json:"trace"`
	Impact         string   `json:"impact"`
	Evidence       []string `json:"evidence"`
	Verification   string   `json:"verification"`
	Reproduction   string   `json:"reproduction"`
	Recommendation string   `json:"recommendation"`
}

func writeJSON(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0600)
}
func newReport(ctx context.Context, workDir, requested, task, resume string, tools []toolchain.Status) (*runReport, error) {
	var nonce [6]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := now.Format("20060102T150405Z") + "-" + hex.EncodeToString(nonce[:])
	dir := requested
	if dir == "" {
		dir = filepath.Join(workDir, ".cyber", "audit", id)
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(workDir, dir)
	}
	dir = filepath.Clean(dir)
	r := &runReport{Directory: dir, ID: id, Workspace: workDir, Goal: task, Resume: resume, Started: now, Status: "running", Tools: tools}
	gitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, "git", "-C", workDir, "rev-parse", "HEAD")
	if out, err := cmd.Output(); err == nil {
		r.Revision = strings.TrimSpace(string(out))
		status, err := exec.CommandContext(gitCtx, "git", "-C", workDir, "status", "--porcelain", "--untracked-files=normal").Output()
		if err == nil {
			dirty := strings.TrimSpace(string(status)) != ""
			r.Dirty = &dirty
		} else {
			r.GitError = "could not read worktree status"
		}
	} else {
		r.GitError = "Git/revision unavailable; auditing supplied files"
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return nil, fmt.Errorf("report directory must be new: %w", err)
	}
	for _, name := range []string{"raw", "evidence"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			return nil, err
		}
	}
	if err := r.save(); err != nil {
		return nil, err
	}
	c := coverage{Scope: task, Examined: []string{}, Excluded: []string{".git", ".cyber", dir}, Unsupported: []string{}, Unresolved: []string{"Audit has not completed."}, Checks: []checkRecord{}}
	if err := writeJSON(filepath.Join(dir, "coverage.json"), c); err != nil {
		return nil, err
	}
	if err := writeJSON(filepath.Join(dir, "findings.json"), []finding{}); err != nil {
		return nil, err
	}
	for name, body := range map[string]string{
		"index.md": "---\nokf_version: \"0.2\"\n---\n\n# Audit in progress\n\nThis report is incomplete. See [coverage](coverage.json), [findings](findings.json), [run metadata](run.json), [investigation log](log.md) and [session evidence](session.jsonl).\n",
		"log.md":   "# Investigation log\n\n- " + now.Format(time.RFC3339) + " Audit started.\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			return nil, err
		}
	}
	return r, nil
}
func (r *runReport) save() error { return writeJSON(filepath.Join(r.Directory, "run.json"), r) }
func (r *runReport) finish(ctx context.Context, runErr error) error {
	runErr = errors.Join(runErr, ctx.Err())
	r.Status = "completed"
	if runErr == nil {
		runErr = r.validate(ctx)
		if runErr != nil {
			r.Status = "incomplete"
		}
	} else {
		r.Status = "failed"
	}
	if runErr != nil {
		r.Error = runErr.Error()
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			r.Status = "interrupted"
		}
	}
	now := time.Now().UTC()
	r.Finished = &now
	return errors.Join(runErr, r.save())
}

// searchExclusions is shared by rg's default config and the model's AST recipes.
func (r *runReport) searchExclusions() (string, string, error) {
	patterns := []string{"!.git/**", "!.cyber/**"}
	if rel, err := filepath.Rel(r.Workspace, r.Directory); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		// Escape glob metacharacters so a custom report path remains literal.
		rel = filepath.ToSlash(rel)
		rel = strings.NewReplacer("[", "[[]", "*", "[*]", "?", "[?]", "{", "[{]", "}", "[}]").Replace(rel)
		patterns = append(patterns, "!"+rel+"/**")
	}
	var config strings.Builder
	for _, pattern := range patterns {
		config.WriteString("--glob\n" + pattern + "\n")
	}
	path := filepath.Join(r.Directory, "ripgrep.conf")
	if err := os.WriteFile(path, []byte(config.String()), 0600); err != nil {
		return "", "", err
	}
	return path, strings.Join(patterns, "\n"), nil
}
func (r *runReport) validate(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var c coverage
	body, err := os.ReadFile(filepath.Join(r.Directory, "coverage.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return fmt.Errorf("coverage.json: %w", err)
	}
	if !c.Reviewed || c.Scope == "" || len(c.Examined) == 0 || c.Excluded == nil || c.Unsupported == nil || c.Unresolved == nil {
		return fmt.Errorf("audit report incomplete: coverage must identify examined scope and remaining limits")
	}
	checks := map[string]bool{}
	for _, check := range c.Checks {
		switch check.Status {
		case "completed", "incomplete", "not_applicable":
		default:
			return fmt.Errorf("invalid check status %q", check.Status)
		}
		if check.Evidence == "" {
			return fmt.Errorf("%s check requires evidence or a reason", check.Tool)
		}
		if check.Status == "completed" {
			if err := reportEvidence(r.Directory, check.Evidence); err != nil {
				return err
			}
		}
		if check.Status == "incomplete" && len(c.Unresolved) == 0 {
			return fmt.Errorf("incomplete check %s requires an unresolved limitation", check.Tool)
		}
		checks[check.Tool] = true
	}
	if !checks["osv-scanner"] || !checks["proton"] {
		return fmt.Errorf("coverage must record SCA and leak checks, including skipped/incomplete reasons")
	}
	body, err = os.ReadFile(filepath.Join(r.Directory, "findings.json"))
	if err != nil {
		return err
	}
	var findings []finding
	if err = json.Unmarshal(body, &findings); err != nil {
		return fmt.Errorf("findings.json: %w", err)
	}
	if findings == nil {
		return fmt.Errorf("findings.json must be an array")
	}
	ids := map[string]bool{}
	for _, f := range findings {
		if f.ID == "" || f.Title == "" || ids[f.ID] {
			return fmt.Errorf("finding requires a unique id and title")
		}
		ids[f.ID] = true
		switch f.Status {
		case "candidate", "confirmed", "dismissed", "inconclusive":
		default:
			return fmt.Errorf("%s: invalid finding status", f.ID)
		}
		switch f.Verification {
		case "static", "reproduced", "not_attempted":
		default:
			return fmt.Errorf("%s: invalid verification", f.ID)
		}
		if f.Status == "confirmed" && (f.Location == "" || f.Preconditions == "" || f.Impact == "" || len(f.Trace) == 0 || len(f.Evidence) == 0 || f.Verification == "not_attempted") {
			return fmt.Errorf("%s: confirmed finding lacks trace/evidence", f.ID)
		}
		if f.Verification == "reproduced" && (f.Reproduction == "" || len(f.Evidence) == 0) {
			return fmt.Errorf("%s: reproduction evidence required", f.ID)
		}
		for _, path := range f.Evidence {
			if err := reportEvidence(r.Directory, path); err != nil {
				return err
			}
		}
	}
	body, err = os.ReadFile(filepath.Join(r.Directory, "index.md"))
	if err != nil {
		return err
	}
	if strings.Contains(string(body), "# Audit in progress") {
		return fmt.Errorf("audit report incomplete: index.md is still a draft")
	}
	validation, err := okf.Validate(ctx, r.Directory, false)
	if err != nil {
		return err
	}
	for _, issue := range validation.Issues {
		if issue.Level == okf.Error {
			return fmt.Errorf("audit report incomplete: OKF %s: %s", issue.Path, issue.Message)
		}
	}
	return ctx.Err()
}
func reportEvidence(dir, path string) error {
	if filepath.IsAbs(path) {
		return fmt.Errorf("evidence must use a report-relative path: %s", path)
	}
	rel := filepath.Clean(filepath.FromSlash(path))
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("evidence must remain in the report: %s", path)
	}
	info, err := os.Stat(filepath.Join(dir, rel))
	if err != nil {
		return fmt.Errorf("missing evidence %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("evidence is not a file: %s", path)
	}
	return nil
}
