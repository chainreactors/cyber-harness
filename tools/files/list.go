package files

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const maxListEntries = 20000

// readDirectory bounds allocation as well as returned entries. fs.ReadDir and
// fs.WalkDir read entire directories before a visitor can enforce a limit.
func readDirectory(ctx context.Context, root *os.Root, name string, remaining int) ([]fs.DirEntry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	directory, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(remaining + 1)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(entries) > remaining {
		return nil, fmt.Errorf("directory traversal limit exceeded")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

func (f *Files) List(ctx context.Context, directory string) ([]fs.DirEntry, error) {
	root, err := f.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer f.release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(f.lifetime, cancel)
	defer stop()
	if f.lifetime.Err() != nil {
		cancel()
	}
	if directory == "" {
		directory = "."
	}
	if !filepath.IsLocal(directory) {
		return nil, fmt.Errorf("ls requires a local path")
	}
	return readDirectory(ctx, root, directory, maxListEntries)
}

// Glob returns at most limit matching paths and bounds traversal independently.
// Patterns use path.Match semantics; ** is not a recursive wildcard.
func (f *Files) Glob(ctx context.Context, pattern string, limit int) ([]string, error) {
	root, err := f.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer f.release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(f.lifetime, cancel)
	defer stop()
	if f.lifetime.Err() != nil {
		cancel()
	}
	if strings.Contains(pattern, "**") {
		return nil, fmt.Errorf("glob does not support **")
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	var result []string
	remaining := maxListEntries
	var walk func(string, bool) error
	walk = func(name string, directory bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ok, _ := path.Match(pattern, name); ok {
			result = append(result, name)
			if len(result) >= limit {
				return fs.SkipAll
			}
		}
		if !directory {
			return nil
		}
		entries, err := readDirectory(ctx, root, filepath.FromSlash(name), remaining)
		if err != nil {
			return err
		}
		remaining -= len(entries)
		for _, entry := range entries {
			if err := walk(path.Join(name, entry.Name()), entry.IsDir()); err != nil {
				return err
			}
		}
		return nil
	}
	err = walk(".", true)
	if err == fs.SkipAll {
		err = nil
	}
	return result, err
}
