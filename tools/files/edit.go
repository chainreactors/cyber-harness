package files

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	filepb "github.com/chainreactors/aiscan/aop/file"
)

type EditPatch struct {
	OldText    string `json:"old_text" jsonschema:"description=Exact original text to replace"`
	NewText    string `json:"new_text" jsonschema:"description=Replacement text"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"description=Replace all original occurrences"`
}

// ApplyEdits resolves every match against the original text, rejects overlaps,
// and builds the result once. Replacement text is never searched.
func ApplyEdits(original string, edits []EditPatch) (string, error) {
	return applyEdits(original, edits, int64(int(^uint(0)>>1)))
}

func applyEdits(original string, edits []EditPatch, maxBytes int64) (string, error) {
	type match struct {
		start, end, edit int
		text             string
	}
	var matches []match
	for i, edit := range edits {
		if edit.OldText == "" {
			return "", fmt.Errorf("edits[%d]: old_text must not be empty", i)
		}
		if edit.OldText == edit.NewText {
			return "", fmt.Errorf("edits[%d]: old_text and new_text are identical", i)
		}
		count := strings.Count(original, edit.OldText)
		if count == 0 {
			return "", fmt.Errorf("edits[%d]: old_text not found", i)
		}
		if count > 1 && !edit.ReplaceAll {
			return "", fmt.Errorf("edits[%d]: old_text matches %d locations; set replace_all or disambiguate", i, count)
		}
		for offset := 0; offset < len(original); {
			index := strings.Index(original[offset:], edit.OldText)
			if index < 0 {
				break
			}
			start := offset + index
			matches = append(matches, match{start, start + len(edit.OldText), i, edit.NewText})
			offset = start + len(edit.OldText)
			if !edit.ReplaceAll {
				break
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].start < matches[j].start })
	for i := 1; i < len(matches); i++ {
		if matches[i].start < matches[i-1].end {
			return "", fmt.Errorf("edits[%d] and edits[%d] overlap", matches[i-1].edit, matches[i].edit)
		}
	}
	// Check the final size before allocating replacement output. Subtract all
	// removed bytes first so edits that shrink and grow are order independent.
	size := int64(len(original))
	for _, m := range matches {
		size -= int64(m.end - m.start)
	}
	if size > maxBytes {
		return "", fmt.Errorf("edited file exceeds %d bytes", maxBytes)
	}
	for _, m := range matches {
		if int64(len(m.text)) > maxBytes-size {
			return "", fmt.Errorf("edited file exceeds %d bytes", maxBytes)
		}
		size += int64(len(m.text))
	}
	var output strings.Builder
	output.Grow(int(size))
	offset := 0
	for _, m := range matches {
		output.WriteString(original[offset:m.start])
		output.WriteString(m.text)
		offset = m.end
	}
	output.WriteString(original[offset:])
	result := output.String()
	if result == original {
		return "", fmt.Errorf("edits produced no changes")
	}
	return result, nil
}

func (f *Files) Edit(ctx context.Context, path string, edits []EditPatch) error {
	f.mutation.Lock()
	defer f.mutation.Unlock()
	_, err := f.acquire(ctx)
	if err != nil {
		return err
	}
	defer f.release()
	var data []byte
	if f.config.ReadOnly {
		err = &os.PathError{Op: "edit", Path: path, Err: os.ErrPermission}
	} else if strings.Contains(path, "://") {
		err = fmt.Errorf("edit requires a local path")
	} else {
		data, err = f.read(ctx, path, false)
	}
	if err == nil {
		var result string
		result, err = applyEdits(string(data), edits, f.config.MaxBytes)
		data = []byte(result)
	}
	if err != nil {
		f.observe(ctx, filepb.AccessOp_ACCESS_OP_EDIT, path, nil, 0, err, uint32(len(edits)))
		return err
	}
	return f.writeBytes(ctx, path, data, filepb.AccessOp_ACCESS_OP_EDIT, uint32(len(edits)))
}
