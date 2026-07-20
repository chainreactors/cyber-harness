package cairnrunner

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func (s *session) handleExec(msg message) {
	var params execParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.respond(msg.ID, false, nil, err)
		return
	}
	if params.Command == "" {
		_ = s.respond(msg.ID, false, nil, fmt.Errorf("command is required"))
		return
	}

	callCtx := output.ContextWithCallID(s.ctx, strconv.FormatUint(uint64(msg.ID), 10))
	runCtx, cancel := context.WithCancel(callCtx)
	s.mu.Lock()
	s.cancels[msg.ID] = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.cancels, msg.ID)
		s.mu.Unlock()
	}()

	options := commands.BashExecOptions{
		WorkDir: resolvePath(params.Cwd),
		Env:     params.Env,
		OnOutput: func(data []byte) {
			_ = s.writeJSON(message{
				T: "exec_out", ID: msg.ID, Stream: "stdout",
				Data: base64.StdEncoding.EncodeToString(data),
			})
		},
	}
	if params.Timeout > 0 {
		options.Timeout = time.Duration(params.Timeout) * time.Second
	}
	info, err := s.client.bash.RunForeground(runCtx, params.Command, options)
	if err != nil {
		_ = s.respond(msg.ID, false, nil, err)
		return
	}
	_ = s.respond(msg.ID, true, execResult{
		ExitCode:  info.ExitCode,
		State:     string(info.State),
		KillCause: info.KillCause,
		Duration:  info.Duration().Seconds(),
	}, nil)
}
