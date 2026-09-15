package host

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/encoding/protojson"
)

// Stdio frames envelopes as protobuf JSONL over caller-owned streams. Like
// aop.EnvelopeStream, it allows one reader and one writer. Host serializes
// concurrent replies and events; the codec owns no connection state.
type Stdio struct {
	scanner *bufio.Scanner
	output  io.Writer
}

func NewStdio(input io.Reader, output io.Writer) *Stdio {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	return &Stdio{scanner: scanner, output: output}
}

func (s *Stdio) Recv() (*aop.Envelope, error) {
	for s.scanner.Scan() {
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}
		envelope := new(aop.Envelope)
		if err := protojson.Unmarshal([]byte(line), envelope); err != nil {
			return nil, fmt.Errorf("decode stdio envelope: %w", err)
		}
		return envelope, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return nil, io.EOF
}

func (s *Stdio) Send(envelope *aop.Envelope) error {
	if s.output == nil {
		return fmt.Errorf("stdio output is required")
	}
	data, err := protojson.Marshal(envelope)
	if err == nil {
		data = append(data, '\n')
		var n int
		n, err = s.output.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
	}
	return err
}
