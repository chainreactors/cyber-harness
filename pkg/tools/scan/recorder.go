package scan

import (
	"os"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/record"
	"github.com/chainreactors/parsers"
)

type recorder struct {
	mu   sync.Mutex
	file *os.File
}

func newRecorder(path string) (*recorder, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &recorder{file: f}, nil
}

func (r *recorder) Close() error {
	if r == nil || r.file == nil {
		return nil
	}
	return r.file.Close()
}

func (r *recorder) write(rec record.Record) {
	if r == nil || r.file == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.file.Write(rec.Marshal())
	r.file.Write([]byte("\n"))
}

func (r *recorder) ScanStart(targets []string, mode string, flags []string) {
	r.write(record.New(record.TypeScanStart, record.ScanStart{
		Targets: targets,
		Mode:    mode,
		Flags:   flags,
	}))
}

func (r *recorder) Service(result *parsers.GOGOResult) {
	if result == nil {
		return
	}
	r.write(record.New(record.TypeService, record.Service{
		Target:   result.GetTarget(),
		Protocol: result.Protocol,
		Banner:   result.Midware,
	}))
}

func (r *recorder) Web(url string, status int, title string, fingers []string) {
	r.write(record.New(record.TypeWeb, record.Web{
		URL:     url,
		Status:  status,
		Title:   title,
		Fingers: fingers,
	}))
}

func (r *recorder) Finding(kind, target, priority, summary, detail string) {
	r.write(record.New(record.TypeFinding, record.Finding{
		Kind:     kind,
		Target:   target,
		Priority: priority,
		Summary:  summary,
		Detail:   detail,
	}))
}

func (r *recorder) AISkill(skill, target, status, summary, detail string, duration time.Duration) {
	r.write(record.New(record.TypeAISkill, record.AISkill{
		Skill:    skill,
		Target:   target,
		Status:   status,
		Summary:  summary,
		Detail:   detail,
		Duration: duration.Seconds(),
	}))
}

func (r *recorder) AITurn(skill string, turn int, reqContent, respContent string, toolCalls []record.ToolCall, duration time.Duration, tokens record.TokenUsage) {
	r.write(record.New(record.TypeAITurn, record.AITurn{
		Skill:     skill,
		Turn:      turn,
		Request:   record.AIMessage{Role: "user", Content: reqContent},
		Response:  record.AIMessage{Role: "assistant", Content: respContent},
		ToolCalls: toolCalls,
		Duration:  duration.Seconds(),
		Tokens:    tokens,
	}))
}

func (r *recorder) ScanEnd(duration time.Duration, targets, services, webs, findings, aiSkills, errors int) {
	r.write(record.New(record.TypeScanEnd, record.ScanEnd{
		Duration: duration.Seconds(),
		Targets:  targets,
		Services: services,
		Webs:     webs,
		Findings: findings,
		AISkills: aiSkills,
		Errors:   errors,
	}))
}
