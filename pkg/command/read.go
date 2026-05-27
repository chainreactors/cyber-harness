package command

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

const (
	defaultReadLineLimit = 2000
	defaultReadByteLimit = 50 * 1024     // 50KB
	maxImageSize         = 20 * 1024 * 1024 // 20MB
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
	return "Read the contents of a file. Returns text with line numbers, or image content for image files (PNG, JPG, GIF, WEBP). For large files, use offset and limit to paginate."
}

type ReadArgs struct {
	Path   string `json:"path"            jsonschema:"description=File path to read (absolute or relative to working directory)"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=1-indexed line number to start reading from (default: 1)"`
	Limit  int    `json:"limit,omitempty"  jsonschema:"description=Maximum number of lines to read (default: 2000)"`
}

func (t *ReadTool) Definition() provider.ToolDefinition {
	return ToolDef("read", t.Description(), ReadArgs{})
}

func (t *ReadTool) ExecutionMode() ExecutionMode { return ExecParallel }

func (t *ReadTool) Execute(ctx context.Context, arguments string) (ToolResult, error) {
	args, err := ParseArgs[ReadArgs](arguments)
	if err != nil {
		return ToolResult{}, err
	}

	if args.Path == "" {
		return ToolResult{}, fmt.Errorf("path is required")
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
		return ToolResult{}, fmt.Errorf("file not found: %s", args.Path)
	}

	if info.IsDir() {
		return ToolResult{}, fmt.Errorf("%s is a directory, not a file", args.Path)
	}

	if mime := detectImageMime(resolved); mime != "" {
		return readImageFile(resolved, args.Path, mime, info.Size())
	}

	if isBinaryFile(resolved) {
		return TextResult(fmt.Sprintf("[binary file: %s (%d bytes)]", args.Path, info.Size())), nil
	}

	return t.readFileLines(resolved, args.Path, args.Offset, args.Limit)
}

func (t *ReadTool) readFileLines(resolved, displayPath string, offset, limit int) (ToolResult, error) {
	f, err := os.Open(resolved)
	if err != nil {
		return ToolResult{}, fmt.Errorf("open file: %w", err)
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
	truncatedByBytes := false
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
		formatted := fmt.Sprintf("%d\t%s\n", lineNum, line)

		if outputBytes+len(formatted) > defaultReadByteLimit && outputLines > 0 {
			truncatedByBytes = true
			continue // keep counting total lines
		}

		sb.WriteString(formatted)
		outputLines++
		outputBytes += len(formatted)
	}

	if err := scanner.Err(); err != nil {
		return ToolResult{}, fmt.Errorf("read file: %w", err)
	}

	// Count remaining lines if we stopped early
	if truncatedByBytes || outputLines >= lineLimit {
		// We already counted during the scan loop above
	}

	content := sb.String()
	endLine := startLine + outputLines - 1
	hasMore := endLine < totalLines

	if hasMore {
		nextOffset := endLine + 1
		content += fmt.Sprintf("\n[lines %d-%d of %d total | next: read with offset=%d]",
			startLine, endLine, totalLines, nextOffset)
	}

	return TextResult(content), nil
}

func (t *ReadTool) readVirtual(args ReadArgs) (ToolResult, error) {
	for _, reader := range t.readers {
		if reader == nil {
			continue
		}
		content, handled, err := reader.ReadVirtual(args.Path)
		if !handled {
			continue
		}
		if err != nil {
			return ToolResult{}, err
		}
		return t.paginateString(content, args.Path, args.Offset, args.Limit), nil
	}
	return ToolResult{}, fmt.Errorf("virtual file not found: %s", args.Path)
}

func (t *ReadTool) tryVirtualFallback(path string) (ToolResult, bool) {
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
	return ToolResult{}, false
}

func (t *ReadTool) paginateString(content, displayPath string, offset, limit int) ToolResult {
	lines := strings.Split(content, "\n")
	totalLines := len(lines)

	startLine := offset
	if startLine <= 0 {
		startLine = 1
	}
	if startLine > totalLines {
		return TextResult(fmt.Sprintf("[offset %d exceeds file line count %d]", startLine, totalLines))
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
		formatted := fmt.Sprintf("%d\t%s\n", i+1, lines[i])
		if outputBytes+len(formatted) > defaultReadByteLimit && i > startLine-1 {
			break
		}
		sb.WriteString(formatted)
		outputBytes += len(formatted)
		actualEnd = i + 1
	}

	result := sb.String()
	if actualEnd < totalLines {
		result += fmt.Sprintf("\n[lines %d-%d of %d total | next: read with offset=%d]",
			startLine, actualEnd, totalLines, actualEnd+1)
	}

	return TextResult(result)
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

func readImageFile(resolved, displayPath, mime string, size int64) (ToolResult, error) {
	if size > maxImageSize {
		return TextResult(fmt.Sprintf("[image too large: %s (%d bytes, max %d)]", displayPath, size, maxImageSize)), nil
	}
	f, err := os.Open(resolved)
	if err != nil {
		return ToolResult{}, fmt.Errorf("open image: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxImageSize+1))
	if err != nil {
		return ToolResult{}, fmt.Errorf("read image: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	return ToolResult{
		Content: []ContentBlock{
			TextBlock(fmt.Sprintf("Read image file [%s] (%d bytes)", mime, len(data))),
			ImageBlock(mime, b64),
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
