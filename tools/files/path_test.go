package files

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSlashPathCanonicalizesSeparators(t *testing.T) {
	sep := string(filepath.Separator)
	for _, tt := range []struct {
		in, want string
	}{
		{"sub/c.txt", "sub/c.txt"},
		{"sub" + sep + "c.txt", "sub/c.txt"},
		{"sub" + sep + "." + sep + "c.txt", "sub/c.txt"},
		{"sub" + sep + sep + "c.txt", "sub/c.txt"},
		{"sub/../a.txt", "a.txt"},
		{filepath.Join("task", "nested", "*.txt"), "task/nested/*.txt"},
		{".", "."},
	} {
		got, err := slashPath(tt.in)
		if err != nil || got != tt.want {
			t.Fatalf("slashPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "..", "../a.txt", "sub/../../a.txt", filepath.Join(t.TempDir(), "a.txt")} {
		if _, err := slashPath(in); err == nil {
			t.Fatalf("slashPath accepted %q", in)
		}
	}
}

func TestFileOpsRoundTripOSAndSlashPaths(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sub", filepath.Join("task", "nested")} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tt := range []struct{ name, body string }{
		{"a.txt", "a"},
		{filepath.Join("sub", "c.txt"), "c"},
		{filepath.Join("task", "hello.txt"), "h"},
		{filepath.Join("task", "nested", "d.txt"), "d"},
	} {
		if err := os.WriteFile(filepath.Join(dir, tt.name), []byte(tt.body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	resource, err := New(Config{Directory: dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f := resource.Files
	set := filesystemSet(t, resource)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	sep := string(filepath.Separator)

	for _, path := range []string{
		"sub/c.txt",
		"sub" + sep + "c.txt",
		"sub" + sep + "." + sep + "c.txt",
		"sub" + sep + sep + "c.txt",
	} {
		body, err := f.Read(ctx, path)
		if err != nil || string(body) != "c" {
			t.Fatalf("read %q: %q %v", path, body, err)
		}
	}
	body, err := f.Read(ctx, "sub"+sep+".."+sep+"a.txt")
	if err != nil || string(body) != "a" {
		t.Fatalf("cleaned parent read: %q %v", body, err)
	}
	for _, path := range []string{"..", "../a.txt", "sub/../../a.txt", filepath.Join(dir, "a.txt")} {
		if _, err := f.Read(ctx, path); err == nil {
			t.Fatalf("read accepted %q", path)
		}
	}

	if err := f.Write(ctx, filepath.Join("task", "nested", "e.txt"), []byte("e")); err != nil {
		t.Fatal(err)
	}
	body, err = f.Read(ctx, "task/nested/e.txt")
	if err != nil || string(body) != "e" {
		t.Fatalf("slash read of os write: %q %v", body, err)
	}
	if err := f.Write(ctx, "task/nested/f.txt", []byte("f")); err != nil {
		t.Fatal(err)
	}
	body, err = f.Read(ctx, filepath.Join("task", "nested", "f.txt"))
	if err != nil || string(body) != "f" {
		t.Fatalf("os read of slash write: %q %v", body, err)
	}
	if err := f.Write(ctx, filepath.Join("missing", "x.txt"), []byte("x")); err == nil {
		t.Fatal("write created a missing parent")
	}

	if err := f.Edit(ctx, filepath.Join("task", "hello.txt"), []EditPatch{{OldText: "h", NewText: "H"}}); err != nil {
		t.Fatal(err)
	}
	body, err = f.Read(ctx, "task/hello.txt")
	if err != nil || string(body) != "H" {
		t.Fatalf("edit via os path: %q %v", body, err)
	}

	data, size, err := f.ReadRange(ctx, filepath.Join("sub", "c.txt"), 0, 1)
	if err != nil || string(data) != "c" || size != 1 {
		t.Fatalf("read range: %q %d %v", data, size, err)
	}

	if err := f.Mkdir(ctx, filepath.Join("made", "inner")); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(ctx, "made/inner/z.txt", []byte("z")); err != nil {
		t.Fatal(err)
	}

	entries, err := f.List(ctx, "task"+sep+"nested")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"d.txt", "e.txt", "f.txt"}) {
		t.Fatalf("list os directory: %v", names)
	}
	if _, err := f.List(ctx, ""); err != nil {
		t.Fatal(err)
	}

	for _, pattern := range []string{
		"task/nested/*.txt",
		filepath.Join("task", "nested", "*.txt"),
		"task" + sep + "." + sep + "nested" + sep + "*.txt",
		filepath.Join("task", "*.txt"),
	} {
		got, err := f.Glob(ctx, pattern, 10)
		if err != nil {
			t.Fatalf("glob %q: %v", pattern, err)
		}
		want := []string{"task/nested/d.txt", "task/nested/e.txt", "task/nested/f.txt"}
		if pattern == filepath.Join("task", "*.txt") || pattern == "task/*.txt" {
			want = []string{"task/hello.txt"}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("glob %q: %v", pattern, got)
		}
		for _, match := range got {
			if _, err := f.Read(ctx, match); err != nil {
				t.Fatalf("read glob slash result %q: %v", match, err)
			}
			if _, err := f.Read(ctx, filepath.FromSlash(match)); err != nil {
				t.Fatalf("read glob os result %q: %v", match, err)
			}
		}
	}
	if _, err := f.Glob(ctx, "task"+sep+"**"+sep+"*.txt", 10); err == nil {
		t.Fatal("accepted ** through an os separator")
	}
}
