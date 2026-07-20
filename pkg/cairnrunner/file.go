package cairnrunner

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (s *session) handleRead(msg message) {
	var params fileParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		_ = s.respond(msg.ID, false, nil, err)
		return
	}
	file, err := os.Open(resolvePath(params.Path))
	if err != nil {
		_ = s.respond(msg.ID, false, nil, err)
		return
	}
	defer file.Close()

	total := int64(0)
	buffer := make([]byte, chunkSize)
	for {
		n, readErr := file.Read(buffer)
		if n > 0 {
			frame := make([]byte, 4+n)
			binary.BigEndian.PutUint32(frame[:4], msg.ID)
			copy(frame[4:], buffer[:n])
			if err := s.writeBinary(frame); err != nil {
				return
			}
			total += int64(n)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = s.respond(msg.ID, false, nil, readErr)
			return
		}
	}
	_ = s.respond(msg.ID, true, map[string]any{"size": total}, nil)
}

func (s *session) finishWrite(id uint32) {
	s.mu.Lock()
	pending := s.writes[id]
	delete(s.writes, id)
	s.mu.Unlock()
	if pending == nil {
		_ = s.respond(id, false, nil, fmt.Errorf("no write in progress"))
		return
	}
	if err := os.MkdirAll(filepath.Dir(pending.path), 0o755); err != nil {
		_ = s.respond(id, false, nil, err)
		return
	}
	if err := os.WriteFile(pending.path, pending.data, 0o644); err != nil {
		_ = s.respond(id, false, nil, err)
		return
	}
	_ = s.respond(id, true, map[string]any{"size": len(pending.data)}, nil)
}
