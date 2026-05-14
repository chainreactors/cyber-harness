package scan

import (
	"fmt"
	"io"

	"github.com/chainreactors/parsers"
)

type webEndpoint struct {
	URL        string
	HostHeader string
	Source     string
}

type fingerprint struct {
	Target string
	Name   string
	Source string
}

type sprayObservation struct {
	Result     *parsers.SprayResult
	Capability string
}

type verificationResult struct {
	Finding verificationFinding
	Source  string
}

type projector struct {
	data          *scanData
	stream        io.Writer
	streamOptions outputOptions
	fileLines     []string
}

type projectorOptions struct {
	Debug       bool
	Stream      io.Writer
	StreamColor bool
}

func newProjector(inputs []string, opts projectorOptions) *projector {
	return &projector{
		data:          newScanData(inputs, opts.Debug),
		stream:        opts.Stream,
		streamOptions: outputOptions{Color: opts.StreamColor},
		fileLines:     make([]string, 0),
	}
}

func (p *projector) Observe(pe pipelineEvent) {
	p.data.Record(pe)

	if pe.Action != pipelineEventAccept {
		return
	}

	plain := formatEventLine(pe.Event, outputOptions{})
	if plain == "" {
		return
	}

	p.data.mu.Lock()
	p.fileLines = append(p.fileLines, plain)
	p.data.mu.Unlock()

	if p.stream == nil {
		return
	}
	line := formatEventLine(pe.Event, p.streamOptions)
	if line != "" {
		fmt.Fprintln(p.stream, line)
	}
}

func (p *projector) Finish() {
	p.data.Finish()
}

func (p *projector) String() string {
	return formatSummary(p.data)
}

func (p *projector) ReportMarkdown() string {
	return formatMarkdown(p.data)
}

func (p *projector) JSONLines() (string, error) {
	return formatJSONLines(p.data)
}

func (p *projector) PlainText() string {
	p.data.mu.Lock()
	lines := append([]string(nil), p.fileLines...)
	p.data.mu.Unlock()
	return formatPlainText(p.data, lines)
}
