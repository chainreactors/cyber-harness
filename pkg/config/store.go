package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// PrepareFile stages a private file beside its destination. Caller owns cleanup.
func PrepareFile(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*.yaml")
	if err != nil {
		return "", err
	}
	name := f.Name()
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if err = f.Chmod(0600); err != nil {
		return "", err
	}
	if _, err = f.Write(data); err != nil {
		return "", err
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

// CommitFile atomically replaces a destination with a prepared file.
func CommitFile(staged, target string) error {
	if err := replaceFile(staged, target); err != nil {
		return err
	}
	if directory, err := os.Open(filepath.Dir(target)); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

// WriteFile never overwrites unless replace is explicit. Force initialization
// retains a unique private backup before committing the new document.
func WriteFile(path string, data []byte, replace, backup bool) (bool, error) {
	if _, err := os.Lstat(path); err == nil && !replace {
		return false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	tmp, err := PrepareFile(path, data)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp)
	if !replace {
		// Linking the complete staged file is atomic and fails if the target exists.
		if err = os.Link(tmp, path); os.IsExist(err) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("create configuration: %w", err)
		}
		return true, nil
	}
	if backup {
		old, readErr := os.ReadFile(path)
		if readErr == nil {
			f, e := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".bak-*")
			if e != nil {
				return false, e
			}
			if e = f.Chmod(0600); e == nil {
				_, e = f.Write(old)
			}
			if e == nil {
				e = f.Sync()
			}
			closeErr := f.Close()
			if e != nil {
				_ = os.Remove(f.Name())
				return false, e
			}
			if closeErr != nil {
				return false, closeErr
			}
		} else if !os.IsNotExist(readErr) {
			return false, readErr
		}
	}
	if err = CommitFile(tmp, path); err != nil {
		return false, err
	}
	return true, nil
}
