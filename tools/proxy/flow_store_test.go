package proxy

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
)

func TestStorageConfigValidation(t *testing.T) {
	for _, c := range []cfg.TrafficOptions{
		{BodyStorage: "memory"}, {BodyMaxBytes: -1}, {BodyRetentionBytes: -1},
		{BodyMaxBytes: 8, BodyRetentionBytes: 15},
	} {
		if _, err := c.Normalize(); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	c, err := (cfg.TrafficOptions{}).Normalize()
	if err != nil || c.BodyStorage != "none" || c.BodyMaxBytes != maxBodyCaptureBytes {
		t.Fatalf("%+v, %v", c, err)
	}
}

func TestDefaultStorageKeepsOnlyPreviewWithoutCaptureDirectory(t *testing.T) {
	dir := t.TempDir()
	hub := NewProxyHub(nil, nil, dir, true)
	if err := hub.Start(dir); err != nil {
		t.Fatal(err)
	}
	defer hub.Shutdown(context.Background())
	target := startTestTarget(maxBodySnip * 32)
	defer target.Close()
	getThrough(t, hubClient(t, hub, ""), target.URL)
	flows := waitForFlows(t, hub.Store(), 1)
	if len(flows) != 1 {
		t.Fatalf("flows=%d", len(flows))
	}
	f := flows[0]
	if hub.Store().files[f.ID][1] >= 0 || len(f.Response.Body) != maxBodySnip || f.Complete || !strings.Contains(f.Error, "preview only") {
		t.Fatalf("unexpected preview: %+v", f)
	}
	if _, err := os.Stat(filepath.Join(dir, "capture")); !os.IsNotExist(err) {
		t.Fatalf("capture directory exists: %v", err)
	}
}

func TestFileConsumerOwnsChunksAndClosesBeforePublication(t *testing.T) {
	store := NewFlowStore(8)
	if err := store.SetBodyDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	hub := NewProxyHub(nil, store, "", true)
	hub.storage.BodyMaxBytes = 8
	stream := &bodyStream{}
	finish, release, err := hub.recordBody(stream)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	p := []byte("abcdefghijk")
	_, _ = stream.Write(p)
	p[0] = 'X'
	stream.Close()
	file, err := finish(false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil || string(data) != "abcdefgh" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, err = file.Write([]byte("x")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("file is not closed: %v", err)
	}
	again, err := finish(false)
	if err != nil || file != again {
		t.Fatal("finalization is not idempotent")
	}
	removeCaptureFiles([2]*os.File{nil, file})
}

func TestBodySubscriberOverflowDoesNotBlockWriter(t *testing.T) {
	stream := &bodyStream{}
	gate := make(chan struct{})
	entered := make(chan struct{})
	sub, err := stream.events.SubscribeAsync(eventbus.SubscribeOptions[[]byte]{Buffer: 1, Clone: func(p []byte) []byte { return bytes.Clone(p) }}, func([]byte) error {
		close(entered)
		<-gate
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Write([]byte("a"))
	<-entered
	done := make(chan struct{})
	go func() { _, _ = stream.Write(make([]byte, 2<<20)); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("disk subscriber blocked producer")
	}
	close(gate)
	stream.Close()
	err = sub.Close(context.Background())
	preview, size := stream.snapshot()
	if !errors.Is(err, eventbus.ErrOverflow) || size != 1+2<<20 || len(preview) != maxBodySnip {
		t.Fatalf("size=%d preview=%d, %v", size, len(preview), err)
	}
}

func TestFlowSubscriptionFiltersBeforeReadingBodies(t *testing.T) {
	hub := NewProxyHub(nil, NewFlowStore(8), "", true)
	delivered := make(chan Flow, 1)
	sub, err := hub.SubscribeFlows(-1, eventbus.SubscribeOptions[Flow]{
		Buffer: 1, Filter: func(f Flow) bool { return f.ToolID == "selected" },
	}, func(f Flow) error { delivered <- f; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Cancel()
	for i := 0; i < 10; i++ {
		hub.ingest(Flow{ToolID: "ignored", Exchange: traffic.Exchange{
			Response: &traffic.Response{},
		}})
	}
	hub.ingest(Flow{ToolID: "selected"})
	select {
	case flow := <-delivered:
		if flow.ToolID != "selected" {
			t.Fatalf("flow=%v", flow)
		}
	case <-time.After(time.Second):
		t.Fatal("filtered subscription did not deliver")
	}
	if err := sub.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestTrafficJournalContainsNoBodyBytesAndDrainsOnClose(t *testing.T) {
	dir := t.TempDir()
	s := NewFlowStore(8)
	if err := s.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	s.Add(Flow{Exchange: traffic.Exchange{Request: traffic.Request{Body: []byte("private-body")}}})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "flows.jsonl"))
	if err != nil || len(data) == 0 {
		t.Fatalf("journal: %q %v", data, err)
	}
	if bytes.Contains(data, []byte("private-body")) || bytes.Contains(data, []byte("cHJpdmF0ZS1ib2R5")) {
		t.Fatal("body leaked into metadata journal")
	}
}

func TestMissingBodyIsReportedAsIncomplete(t *testing.T) {
	s := NewFlowStore(1)
	if err := s.SetBodyDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(s.BodyDir(), "body", "capture-missing")
	if err := os.WriteFile(path, []byte("body"), 0600); err != nil {
		t.Fatal(err)
	}
	f := addTestBody(t, s, Flow{Exchange: traffic.Exchange{Complete: true, Response: &traffic.Response{StatusCode: 200}}}, path)
	if err := os.Remove(s.bodyPath(f.ID, 1)); err != nil {
		t.Fatal(err)
	}
	wire := s.flowToProto(&f)
	if wire.Complete || !strings.Contains(wire.Error, "body unavailable") {
		t.Fatalf("wire=%v", wire)
	}
}

func TestClearPreservesUnownedAndInFlightFiles(t *testing.T) {
	dir := t.TempDir()
	s := NewFlowStore(8)
	if err := s.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	path := filepath.Join(dir, "unrelated.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s.Clear()
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	s.Add(Flow{})
}
