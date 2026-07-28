package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/truncate"
)

const (
	defaultReadLineLimit = truncate.DefaultMaxLines
	defaultReadByteLimit = truncate.DefaultMaxBytes
	maxImageSize         = truncate.MaxImageSize
)

type ReadTool struct {
	workDir string
	readers []VirtualFileReader
}

type VirtualFileReader interface {
	ReadVirtual(path string) (content string, handled bool, err error)
}

func NewReadTool(workDir string, readers ...VirtualFileReader) *ReadTool {
	return &ReadTool{workDir: workDir, readers: readers}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file. Returns raw text content, or image content for image files (PNG, JPG, GIF, WEBP). For large files, use offset and limit to paginate."
}

type ReadArgs struct {
	Path   string `json:"path"            jsonschema:"description=File path to read (absolute or relative to working directory)"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=1-indexed line number to start reading from (default: 1)"`
	Limit  int    `json:"limit,omitempty"  jsonschema:"description=Maximum number of lines to read (default: 2000)"`
}

func (t *ReadTool) Definition() coretool.Definition {
	return coretool.Def("read", t.Description(), ReadArgs{})
}

func (t *ReadTool) Execute(ctx context.Context, arguments string) (coretool.Result, error) {
	effective := *t
	effective.workDir = coretool.WorkDirFromContext(ctx, t.workDir)
	t = &effective
	args, err := coretool.ParseArgs[ReadArgs](arguments)
	if err != nil {
		return coretool.Result{}, err
	}

	if args.Path == "" {
		return coretool.Result{}, fmt.Errorf("path is required")
	}

	// Virtual file reads (aiscan://..., embedded skills, etc.)
	if strings.Contains(args.Path, "://") {
		return t.readVirtual(args)
	}

	resolved := t.resolvePath(args.Path)

	// Try filesystem first
	info, err := os.Stat(resolved)
	if err != nil {
		// Fallback to virtual readers for bare paths
		if result, ok := t.tryVirtualFallback(args.Path); ok {
			return result, nil
		}
		return coretool.Result{}, fmt.Errorf("file not found: %s", args.Path)
	}

	if info.IsDir() {
		return coretool.Result{}, fmt.Errorf("%s is a directory, not a file", args.Path)
	}

	if mime := detectImageMime(resolved); mime != "" {
		return readImageFile(resolved, args.Path, mime, info.Size())
	}

	if isBinaryFile(resolved) {
		return coretool.TextResult(fmt.Sprintf("[binary file: %s (%d bytes)]", args.Path, info.Size())), nil
	}

	return t.readFileLines(resolved, args.Path, args.Offset, args.Limit)
}

func (t *ReadTool) readFileLines(resolved, displayPath string, offset, limit int) (coretool.Result, error) {
	f, err := os.Open(resolved)
	if err != nil {
		return coretool.Result{}, fmt.Errorf("open file: %w", err)
	}
	defer f.Close()

	// Normalize: offset is 1-indexed, 0 means "from beginning"
	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}

	lineLimit := limit
	if lineLimit <= 0 {
		lineLimit = defaultReadLineLimit
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var sb strings.Builder
	lineNum := 0
	outputLines := 0
	outputBytes := 0
	totalLines := 0

	for scanner.Scan() {
		lineNum++
		totalLines = lineNum

		if lineNum < startLine {
			continue
		}

		if outputLines >= lineLimit {
			continue // keep counting total lines
		}

		line := scanner.Text()

		if outputBytes+len(line)+1 > defaultReadByteLimit && outputLines > 0 {
			continue // keep counting total lines
		}

		sb.WriteString(line)
		sb.WriteByte('\n')
		outputLines++
		outputBytes += len(line) + 1
	}

	if err := scanner.Err(); err != nil {
		return coretool.Result{}, fmt.Errorf("read file: %w", err)
	}

	content := sb.String()
	endLine := startLine + outputLines - 1
	hasMore := endLine < totalLines

	if hasMore {
		nextOffset := endLine + 1
		content += fmt.Sprintf("\n[lines %d-%d of %d total | next: read with offset=%d]",
			startLine, endLine, totalLines, nextOffset)
	}

	return coretool.TextResult(content), nil
}

func (t *ReadTool) readVirtual(args ReadArgs) (coretool.Result, error) {
	for _, reader := range t.readers {
		if reader == nil {
			continue
		}
		content, handled, err := reader.ReadVirtual(args.Path)
		if !handled {
			continue
		}
		if err != nil {
			return coretool.Result{}, err
		}
		return t.paginateString(content, args.Path, args.Offset, args.Limit), nil
	}
	return coretool.Result{}, fmt.Errorf("virtual file not found: %s", args.Path)
}

func (t *ReadTool) tryVirtualFallback(path string) (coretool.Result, bool) {
	for _, reader := range t.readers {
		if reader == nil {
			continue
		}
		content, handled, err := reader.ReadVirtual(path)
		if !handled {
			continue
		}
		if err != nil {
			continue
		}
		return t.paginateString(content, path, 0, 0), true
	}
	return coretool.Result{}, false
}

func (t *ReadTool) paginateString(content, displayPath string, offset, limit int) coretool.Result {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	if startLine > totalLines {
		return coretool.TextResult(fmt.Sprintf("[offset %d exceeds file line count %d]", startLine, totalLines))
	}

	lineLimit := limit
	if lineLimit <= 0 {
		lineLimit = defaultReadLineLimit
	}

	endIdx := startLine - 1 + lineLimit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var sb strings.Builder
	outputBytes := 0
	actualEnd := startLine - 1
	for i := startLine - 1; i < endIdx; i++ {
		line := lines[i]
		if outputBytes+len(line)+1 > defaultReadByteLimit && i > startLine-1 {
			break
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
		outputBytes += len(line) + 1
		actualEnd = i + 1
	}

	result := sb.String()
	if actualEnd < totalLines {
		result += fmt.Sprintf("\n[lines %d-%d of %d total | next: read with offset=%d]",
			startLine, actualEnd, totalLines, actualEnd+1)
	}

	return coretool.TextResult(result)
}

func (t *ReadTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.workDir, path)
}

const imageSniffSize = 12

func detectImageMime(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	buf := make([]byte, imageSniffSize)
	n, _ := f.Read(buf)
	if n < 4 {
		return ""
	}
	buf = buf[:n]

	if buf[0] == 0xFF && buf[1] == 0xD8 && buf[2] == 0xFF {
		return "image/jpeg"
	}
	if buf[0] == 0x89 && buf[1] == 'P' && buf[2] == 'N' && buf[3] == 'G' {
		return "image/png"
	}
	if buf[0] == 'G' && buf[1] == 'I' && buf[2] == 'F' {
		return "image/gif"
	}
	if n >= 12 && buf[0] == 'R' && buf[1] == 'I' && buf[2] == 'F' && buf[3] == 'F' &&
		buf[8] == 'W' && buf[9] == 'E' && buf[10] == 'B' && buf[11] == 'P' {
		return "image/webp"
	}
	return ""
}

func readImageFile(resolved, displayPath, mime string, size int64) (coretool.Result, error) {
	if size > maxImageSize {
		return coretool.TextResult(fmt.Sprintf("[image too large: %s (%d bytes, max %d)]", displayPath, size, maxImageSize)), nil
	}
	f, err := os.Open(resolved)
	if err != nil {
		return coretool.Result{}, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	opt, err := optimizeImage(f, mime)
	if err != nil {
		return coretool.Result{}, fmt.Errorf("optimize image: %w", err)
	}

	desc := fmt.Sprintf("Read image file [%s] (%d bytes)", opt.MimeType, len(opt.Base64Data)*3/4)
	if opt.OrigW > 0 && (opt.OrigW != opt.FinalW || opt.OrigH != opt.FinalH) {
		desc = fmt.Sprintf("Read image file [%s] (original %dx%d, resized to %dx%d)",
			opt.MimeType, opt.OrigW, opt.OrigH, opt.FinalW, opt.FinalH)
	}

	return coretool.Result{
		Content: []coretool.ContentBlock{
			coretool.TextBlock(desc),
			coretool.ImageBlock(opt.MimeType, opt.Base64Data),
		},
	}, nil
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 8*1024)
	n, _ := f.Read(buf)
	if n == 0 {
		return false
	}
	buf = buf[:n]

	// Check for null bytes (strong binary indicator)
	for _, b := range buf {
		if b == 0 {
			return true
		}
	}

	// Check if content is valid UTF-8
	if !utf8.Valid(buf) {
		return true
	}

	return false
}
