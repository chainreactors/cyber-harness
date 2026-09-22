package files

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestConfiguredMountsUseSpecificPrefixes(t *testing.T) {
	config := Config{Directory: t.TempDir(), Mounts: map[string]fs.FS{
		"cyber://":        fstest.MapFS{"skills/doc.md": &fstest.MapFile{Data: []byte("fallback")}},
		"cyber://skills/": fstest.MapFS{"doc.md": &fstest.MapFile{Data: []byte("specific")}},
	}}
	resource, err := New(config, nil)
	if err != nil {
		t.Fatal(err)
	}
	delete(config.Mounts, "cyber://skills/") // construction owns its config snapshot
	if err := resource.Open(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	data, err := resource.Files.Read(t.Context(), "cyber://skills/doc.md")
	if err != nil || string(data) != "specific" {
		t.Fatalf("read=%q err=%v", data, err)
	}
	for _, name := range []string{"cyber://skills/../doc.md", "cyber://skills//doc.md", "cyber://skillshadow/doc.md"} {
		if _, err := resource.Files.Read(t.Context(), name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := resource.Files.Unmount(t.Context(), "cyber://skills/"); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Files.Read(t.Context(), "cyber://skills/doc.md"); err == nil {
		t.Fatal("revoked mount fell back to broader prefix")
	}
}

type gatedMount struct {
	fs.FS
	entered, release chan struct{}
}

func (g gatedMount) Open(name string) (fs.File, error) {
	close(g.entered)
	<-g.release
	return g.FS.Open(name)
}

func TestUnmountRevokesAndRetainsInFlightSource(t *testing.T) {
	resource, _ := New(Config{Directory: t.TempDir()}, nil)
	f := resource.Files
	fSet := filesystemSet(t, resource)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	source := gatedMount{FS: fstest.MapFS{"SKILL.md": &fstest.MapFile{Data: []byte("instructions")}}, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-source.release:
		default:
			close(source.release)
		}
	}()
	if err := f.Mount("skill://", source); err != nil {
		t.Fatal(err)
	}
	readDone := make(chan error, 1)
	go func() {
		data, err := f.Read(context.Background(), "skill://SKILL.md")
		if err == nil && string(data) != "instructions" {
			err = errors.New("wrong mounted data")
		}
		readDone <- err
	}()
	<-source.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.Unmount(ctx, "skill://"); !errors.Is(err, context.Canceled) {
		t.Fatalf("unmount released shared source: %v", err)
	}
	if _, err := f.Read(t.Context(), "skill://SKILL.md"); err == nil {
		t.Fatal("admitted after unmount")
	}
	if f.mounts["skill://"].source == nil {
		t.Fatal("source released before read completed")
	}
	close(source.release)
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := f.Unmount(t.Context(), "skill://"); err != nil {
		t.Fatal(err)
	}
	if err := f.Mount("skill://", fstest.MapFS{}); err == nil {
		t.Fatal("reused reserved mount")
	}
}

func TestMountedReadsRespectPolicyAndLifetime(t *testing.T) {
	resource, _ := New(Config{Directory: t.TempDir(), MaxBytes: 4}, nil)
	f := resource.Files
	fSet := filesystemSet(t, resource)
	if err := fSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer fSet.Close(context.Background())
	if err := f.Mount("skill://", fstest.MapFS{"small": &fstest.MapFile{Data: []byte("ok")}, "large": &fstest.MapFile{Data: []byte("large")}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"skill://../small", "skill:///small", "skill://large", "absent://small"} {
		if _, err := f.Read(t.Context(), name); err == nil {
			t.Fatalf("accepted %s", name)
		}
	}
	if err := f.Write(t.Context(), "skill://small", []byte("x")); err == nil {
		t.Fatal("wrote mounted file")
	}
	source := gatedMount{FS: fstest.MapFS{"small": &fstest.MapFile{Data: []byte("ok")}}, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-source.release:
		default:
			close(source.release)
		}
	}()
	if err := f.Mount("other://", source); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := f.Read(context.Background(), "other://small"); done <- err }()
	<-source.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := resource.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("close: %v", err)
	}
	close(source.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("mount ignored FS cancellation: %v", err)
	}
	if err := fSet.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
