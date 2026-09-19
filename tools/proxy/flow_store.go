package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	operationpb "github.com/chainreactors/cyber/aop/operation"
	traffic "github.com/chainreactors/cyber/aop/traffic"
	"github.com/chainreactors/cyber/core/eventbus"
)

// FlowStore owns completed metadata and its optional files. The fixed pair is
// request/response byte counts; -1 means no file. Paths never enter Flow.
type FlowStore struct {
	publishMu               sync.Mutex
	bodyMu                  sync.RWMutex
	mu                      sync.RWMutex
	events                  eventbus.Bus[Flow]
	flows                   []Flow
	head, size, seq, cap    int
	files                   map[string][2]int64
	bodyBytes, maxBodyBytes int64
	bodyDir                 string
	closed                  bool
	indexSub                *eventbus.Subscription[Flow]
	indexMu                 sync.Mutex
	indexFile               *os.File
	indexErr                error
}

func NewFlowStore(cap int) *FlowStore { return NewFlowStoreWithLimits(cap, defaultMaxBodyBytes) }
func NewFlowStoreWithLimits(cap int, maxBodyBytes int64) *FlowStore {
	if cap <= 0 {
		cap = 10000
	}
	return &FlowStore{cap: cap, flows: make([]Flow, cap), files: make(map[string][2]int64), maxBodyBytes: maxBodyBytes}
}

func bodyFileName(id string, side int) string {
	suffix := ".req"
	if side == 1 {
		suffix = ".resp"
	}
	return id + suffix
}
func (s *FlowStore) bodyPath(id string, side int) string {
	return filepath.Join(s.bodyDir, "body", bodyFileName(id, side))
}
func storedBytes(sizes [2]int64) int64 { return max(sizes[0], 0) + max(sizes[1], 0) }

func (s *FlowStore) BodyDir() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.bodyDir }
func (s *FlowStore) Sequence() int   { s.mu.RLock(); defer s.mu.RUnlock(); return s.seq }
func (s *FlowStore) Count() int      { s.mu.RLock(); defer s.mu.RUnlock(); return s.size }

// Store lock and body lock are held by the caller; publishers serialize file
// ownership with lookup/read so eviction cannot delete a file during a read.
func (s *FlowStore) putLocked(f Flow, sizes [2]int64) {
	incoming := storedBytes(sizes)
	for s.size > 0 && (s.size == s.cap || (s.maxBodyBytes > 0 && s.bodyBytes+incoming > s.maxBodyBytes)) {
		s.evictLocked()
	}
	s.flows[(s.head+s.size)%s.cap] = f
	s.size++
	s.files[f.Id] = sizes
	s.bodyBytes += incoming
	s.seq = max(s.seq, flowSequence(f.Id))
}

func (s *FlowStore) evictLocked() {
	victim := s.flows[s.head]
	previous := s.files[victim.Id]
	s.bodyBytes -= storedBytes(previous)
	delete(s.files, victim.Id)
	if s.bodyDir != "" {
		for side, size := range previous {
			if size >= 0 {
				_ = os.Remove(s.bodyPath(victim.Id, side))
			}
		}
	}
	s.flows[s.head] = Flow{}
	s.head = (s.head + 1) % s.cap
	s.size--
}

func (s *FlowStore) Add(f Flow) Flow { return s.addFiles(f, [2]*os.File{}) }

// addFiles takes ownership of closed temporary files from the Cyber adapter.
// Only this store assigns final names; no arbitrary path travels on the bus.
func (s *FlowStore) addFiles(f Flow, files [2]*os.File) Flow {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	s.bodyMu.Lock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		s.bodyMu.Unlock()
		removeCaptureFiles(files)
		return f
	}
	f = cloneFlowMetadata(f)
	if f.Request == nil {
		f.Request = &traffic.HttpRequest{}
	}
	s.seq++
	f.Id = strconv.Itoa(s.seq)
	sizes := [2]int64{-1, -1}
	var fileErr error
	for side, file := range files {
		if file == nil {
			continue
		}
		source := file.Name()
		if s.bodyDir == "" {
			fileErr = errors.Join(fileErr, errors.New("traffic: body storage disabled"))
			_ = os.Remove(source)
			continue
		}
		info, err := os.Stat(source)
		if err == nil {
			err = os.Rename(source, s.bodyPath(f.Id, side))
		}
		if err != nil {
			fileErr = errors.Join(fileErr, err)
			_ = os.Remove(source)
		} else {
			sizes[side] = info.Size()
		}
	}
	if fileErr != nil {
		f.Complete = false
		f.Error = strings.TrimPrefix(f.Error+"; body storage: "+fileErr.Error(), "; ")
	}
	trim := func(body []byte, side int) []byte {
		if len(body) <= maxBodySnip {
			return body
		}
		if sizes[side] < 0 {
			f.Complete = false
			f.Error = strings.TrimPrefix(f.Error+"; body truncated (preview only)", "; ")
		}
		return body[:maxBodySnip]
	}
	if f.Request != nil {
		f.Request.Body = trim(f.Request.Body, 0)
	}
	if f.Response != nil {
		f.Response.Body = trim(f.Response.Body, 1)
	}
	s.putLocked(f, sizes)
	s.mu.Unlock()
	s.bodyMu.Unlock()
	s.events.Emit(f)
	return f
}

func (s *FlowStore) Query(opts QueryOpts) []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Flow, 0, s.size)
	for n := 0; n < s.size; n++ {
		idx := (s.head + n) % s.cap
		f := &s.flows[idx]
		if opts.Host != "" && !strings.Contains(strings.ToLower(f.Host), strings.ToLower(opts.Host)) {
			continue
		}
		if opts.Status != "" {
			if f.Response == nil || !matchStatus(int(f.Response.StatusCode), opts.Status) {
				continue
			}
		}
		if opts.CType != "" && !strings.Contains(strings.ToLower(f.ContentType), strings.ToLower(opts.CType)) {
			continue
		}
		result = append(result, cloneFlowMetadata(*f))
	}
	if opts.Last > 0 && len(result) > opts.Last {
		result = result[len(result)-opts.Last:]
	}
	return result
}

func (s *FlowStore) hydrate(flow *Flow) error {
	if flow == nil {
		return nil
	}
	s.bodyMu.RLock()
	defer s.bodyMu.RUnlock()
	return s.readBodies(flow)
}

// bodyMu is held by the caller. Open/read happens only at the query or send
// boundary, never while a Flow is queued.
func (s *FlowStore) readBodies(flow *Flow) error {
	s.mu.RLock()
	sizes, ok := s.files[flow.Id]
	dir := s.bodyDir
	s.mu.RUnlock()
	if dir == "" {
		return nil
	}
	if !ok {
		return fmt.Errorf("flow %s is no longer retained", flow.Id)
	}
	for side, size := range sizes {
		if size < 0 {
			continue
		}
		file, err := os.Open(filepath.Join(dir, "body", bodyFileName(flow.Id, side)))
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, size+1))
		err = errors.Join(readErr, file.Close())
		if err != nil {
			return err
		}
		if int64(len(data)) != size {
			return fmt.Errorf("body size changed for flow %s", flow.Id)
		}
		if side == 0 && flow.Request != nil {
			flow.Request.Body = data
		} else if flow.Response != nil {
			flow.Response.Body = data
		}
	}
	return nil
}

func (s *FlowStore) Get(id int) *Flow {
	s.bodyMu.RLock()
	defer s.bodyMu.RUnlock()
	s.mu.RLock()
	var found *Flow
	for n := 0; n < s.size; n++ {
		f := s.flows[(s.head+n)%s.cap]
		if f.Id == strconv.Itoa(id) {
			copy := cloneFlowMetadata(f)
			found = &copy
			break
		}
	}
	s.mu.RUnlock()
	if found != nil {
		if err := s.readBodies(found); err != nil {
			found.Complete = false
			found.Error = strings.TrimPrefix(found.Error+"; body unavailable: "+err.Error(), "; ")
		}
	}
	return found
}

func (s *FlowStore) subscribeIndex() error {
	var err error
	s.indexSub, err = s.events.SubscribeAsync(eventbus.SubscribeOptions[Flow]{
		Buffer: 256, MaxBytes: 4 << 20, Size: flowMetadataSize, Clone: cloneFlowMetadata,
	}, s.appendIndex)
	return err
}

func (s *FlowStore) appendIndex(f Flow) error {
	s.mu.RLock()
	sizes, ok := s.files[f.Id]
	oldest := ""
	if s.size > 0 {
		oldest = s.flows[s.head].Id
	}
	s.mu.RUnlock()
	if !ok {
		return nil // already evicted; do not invent a preview-only disk record
	}
	f = cloneFlowMetadata(f)
	if f.Request != nil {
		f.Request.Body = nil
	}
	if f.Response != nil {
		f.Response.Body = nil
	}
	record := flowIndexRecord{
		Operation: f.Operation, Host: f.Host,
		ContentType: f.ContentType, Duration: int64(f.Duration), TLS: f.TLS,
		Flow: f.Flow, BodySizes: &sizes, OldestID: oldest,
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.indexFile == nil {
		return io.ErrClosedPipe
	}
	data = append(data, '\n')
	n, err := s.indexFile.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		s.indexErr = err
	}
	return err
}

func (s *FlowStore) IndexError() error {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.indexSub != nil {
		if err := s.indexSub.Err(); err != nil {
			return err
		}
	}
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	return s.indexErr
}

func (s *FlowStore) SetBodyDir(dir string) error {
	if dir == "" {
		return nil
	}
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	s.mu.Lock()
	if s.bodyDir != "" || s.closed || s.size != 0 {
		s.mu.Unlock()
		return errors.New("traffic: body directory must be set once before capture")
	}
	s.mu.Unlock()
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(absolute, "body"), 0700); err != nil {
		return err
	}
	s.mu.Lock()
	s.bodyDir = absolute
	s.mu.Unlock()
	path := filepath.Join(absolute, "flows.jsonl")
	if err = s.loadIndex(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	s.indexMu.Lock()
	s.indexFile = file
	s.indexMu.Unlock()
	// Only sweep names this implementation owns. User files remain untouched.
	entries, err := os.ReadDir(filepath.Join(absolute, "body"))
	if err != nil {
		_ = file.Close()
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		id := strings.TrimSuffix(strings.TrimSuffix(name, ".req"), ".resp")
		numeric := flowSequence(id) > 0 && (name == bodyFileName(id, 0) || name == bodyFileName(id, 1))
		s.seq = max(s.seq, flowSequence(id))
		sizes, live := s.files[id]
		side := 0
		if strings.HasSuffix(name, ".resp") {
			side = 1
		}
		if strings.HasPrefix(name, "capture-") || (numeric && (!live || sizes[side] < 0)) {
			if err := os.Remove(filepath.Join(absolute, "body", name)); err != nil {
				_ = file.Close()
				return err
			}
		}
	}
	return s.subscribeIndex()
}

type flowIndexRecord struct {
	Sequence    *int             `json:"sequence,omitempty"`
	Operation   *operationpb.Ref `json:"operation,omitempty"`
	Host        string           `json:"host,omitempty"`
	ContentType string           `json:"content_type,omitempty"`
	Duration    int64            `json:"duration,omitempty"`
	TLS         bool             `json:"tls,omitempty"`
	Flow        *traffic.Flow    `json:"flow,omitempty"`
	BodySizes   *[2]int64        `json:"body_sizes,omitempty"`
	OldestID    string           `json:"oldest_id,omitempty"`
}

func (s *FlowStore) loadIndex(path string) error {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(file)
	var validEnd int64
	for {
		var record flowIndexRecord
		err = decoder.Decode(&record)
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = file.Close()
			// Repair the torn tail before future appends; otherwise every subsequent
			// restart would lose all records following the same damaged JSON.
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return os.Truncate(path, validEnd)
			}
			return fmt.Errorf("traffic: invalid metadata index: %w", err)
		}
		validEnd = decoder.InputOffset()
		if record.Sequence != nil {
			s.mu.Lock()
			s.seq = max(s.seq, *record.Sequence)
			s.mu.Unlock()
			if record.Flow != nil {
				_ = file.Close()
				return errors.New("traffic: index sequence record contains a flow")
			}
			continue
		}
		if record.Flow == nil || flowSequence(record.Flow.GetId()) <= 0 || record.BodySizes == nil {
			_ = file.Close()
			return errors.New("traffic: invalid metadata index record")
		}
		f := Flow{
			Operation: record.Operation, Host: record.Host,
			ContentType: record.ContentType, Duration: time.Duration(record.Duration),
			TLS: record.TLS, Flow: record.Flow,
		}
		s.mu.Lock()
		for s.size > 0 && flowSequence(s.flows[s.head].Id) < flowSequence(record.OldestID) {
			s.evictLocked()
		}
		s.mu.Unlock()
		sizes := *record.BodySizes
		for _, size := range sizes {
			if size < -1 {
				_ = file.Close()
				return errors.New("traffic: invalid stored body size")
			}
		}
		s.mu.Lock()
		s.putLocked(f, sizes)
		s.mu.Unlock()
	}
	return file.Close()
}

func (s *FlowStore) Clear() {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if s.indexSub != nil {
		_ = s.indexSub.Close(context.Background())
	}
	s.bodyMu.Lock()
	defer s.bodyMu.Unlock()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	for id, sizes := range s.files {
		for side, size := range sizes {
			if size >= 0 {
				_ = os.Remove(s.bodyPath(id, side))
			}
		}
	}
	clear(s.flows)
	clear(s.files)
	s.head = 0
	s.size = 0
	s.bodyBytes = 0
	dir := s.bodyDir
	sequence := s.seq
	s.mu.Unlock()
	s.indexMu.Lock()
	if s.indexFile != nil {
		_ = s.indexFile.Close()
		s.indexFile = nil
	}
	s.indexErr = nil
	if dir != "" {
		s.indexFile, s.indexErr = os.OpenFile(filepath.Join(dir, "flows.jsonl"), os.O_CREATE|os.O_TRUNC|os.O_APPEND|os.O_WRONLY, 0600)
		if s.indexErr == nil {
			s.indexErr = json.NewEncoder(s.indexFile).Encode(map[string]int{"sequence": sequence})
		}
	}
	s.indexMu.Unlock()
	if dir != "" {
		_ = s.subscribeIndex()
	}
}

func (s *FlowStore) Close() error { return s.closeContext(context.Background()) }
func (s *FlowStore) closeContext(ctx context.Context) error {
	s.publishMu.Lock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	sub := s.indexSub
	s.publishMu.Unlock()
	var err error
	if sub != nil {
		if err := sub.Close(ctx); err != nil {
			return err
		}
	}
	// Only the caller that takes the drained subscription reports its terminal
	// processing error. A timed-out caller leaves ownership here for retry.
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	if sub != nil && s.indexSub == sub {
		err = sub.Err()
		s.indexSub = nil
		s.indexMu.Lock()
		if s.indexErr == nil {
			s.indexErr = err
		}
		s.indexMu.Unlock()
	}
	return errors.Join(err, s.closeIndex())
}
func (s *FlowStore) closeIndex() error {
	s.indexMu.Lock()
	defer s.indexMu.Unlock()
	if s.indexFile == nil {
		return nil
	}
	file := s.indexFile
	s.indexFile = nil
	return errors.Join(file.Sync(), file.Close())
}
