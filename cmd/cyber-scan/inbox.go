package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	taskcli "github.com/chainreactors/cyber/pkg/cli/task"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/profile"
)

func runInbox(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer, outputTask *taskcli.Output) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, _, err := loadAgentProfile(ctx, newProfile, option, logger, &agentsession.Config{Loop: agent.StandardLoop{}})
	if err != nil {
		return err
	}
	finish := profile.CloseOnce(ctx, p.Close)
	defer func() { runErr = finish(runErr) }()
	stream, err := p.Events()
	if err != nil {
		return err
	}
	outputTask.Observe(stream)
	if option.OutputFormat != "" && option.OutputFormat != "text" {
		output = io.Discard
	}
	active, ok := p.(interface{ InboxRouter() *inboxext.Router })
	if !ok || active.InboxRouter() == nil {
		return fmt.Errorf("profile does not provide an inbox router")
	}
	router := active.InboxRouter()
	submit := func(text string) error {
		message := mailbox.NewUserMessage(text)
		for {
			_, err := router.Submit(ctx, message)
			if err == nil {
				return nil
			}
			if !errors.Is(err, mailbox.ErrInboxFull) {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	if cfg.HasAgentTaskInput(option) {
		text, err := cfg.ResolveTask(option)
		if err != nil {
			return err
		}
		if err = submit(text); err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(output, "Inbox router listening. /exit stops the host; !tmux ls lists managed agents.")
	readDone := make(chan error, 1)
	readExited := make(chan struct{})
	stopRead := func() {}
	var closeInput func()
	if closer, ok := input.(io.ReadCloser); ok {
		stop := context.AfterFunc(ctx, func() { _ = closer.Close() })
		stopRead = func() { stop() }
		closeInput = func() { _ = closer.Close() }
	}
	defer func() {
		cancel()
		if closeInput != nil {
			closeInput()
			<-readExited
		}
		stopRead()
	}()
	go func() {
		defer close(readExited)
		scanner := bufio.NewScanner(input)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "/exit" {
				cancel()
				break
			}
			if strings.HasPrefix(line, "!") {
				host, ok := p.(shellHost)
				if !ok {
					continue
				}
				_, bash := host.Shell()
				arguments, _ := json.Marshal(map[string]any{"command": strings.TrimPrefix(line, "!")})
				result, err := bash.Execute(ctx, string(arguments))
				if err != nil {
					logger.Warnf("command: %v", err)
				} else {
					logger.Importantf("%s", coretool.ResultText(result))
				}
				continue
			}
			if strings.TrimSpace(line) != "" {
				if err := submit(line); err != nil {
					readDone <- err
					return
				}
			}
		}
		readDone <- scanner.Err()
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-router.Done():
			return nil
		case err := <-readDone:
			if err != nil && ctx.Err() == nil {
				return err
			}
			// Detaching the input stream is not stopping an event-driven host.
			readDone = nil
		}
	}
}
