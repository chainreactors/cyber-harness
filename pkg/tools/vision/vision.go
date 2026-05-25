package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/proxyclient"
)

const (
	visionTimeout   = 120 * time.Second
	maxImageSize    = 20 * 1024 * 1024 // 20 MB
	visionMaxTokens = 2048
)

// Command implements command.PseudoCommand for vision analysis.
type Command struct {
	config *provider.ProviderConfig
	client *http.Client
}

// New creates a vision pseudo-command.  Passing a nil config is safe —
// Execute will return an error explaining that no provider is configured.
func New(cfg *provider.ProviderConfig) *Command {
	transport := &http.Transport{}
	if cfg != nil && cfg.Proxy != "" {
		if proxyURL, err := url.Parse(cfg.Proxy); err == nil {
			if dial, dialErr := proxyclient.NewClient(proxyURL); dialErr == nil {
				transport.DialContext = dial.DialContext
			}
		}
	}
	return &Command{
		config: cfg,
		client: &http.Client{
			Transport: transport,
			Timeout:   visionTimeout,
		},
	}
}

func (c *Command) Name() string { return "vision" }

func (c *Command) Usage() string {
	return `vision - Analyze an image file using a vision-capable LLM
Usage:
  vision <image_path> <prompt...>
  vision <image_path> --prompt <text>

The image must be a local file (PNG, JPG, JPEG, GIF, WEBP, BMP). If you need
to analyze a remote image, download it first, then pass the local path.

Options:
  --prompt <text>   What to analyze (alternative to positional prompt args)

Examples:
  vision screenshot.png Read all visible text
  vision /tmp/captcha.png "Solve this CAPTCHA"
  vision topology.png --prompt "Describe the network topology"`
}

// Execute parses CLI-style arguments and performs vision analysis.
//
//	vision <image_path> <prompt words...>
//	vision <image_path> --prompt "prompt text"
func (c *Command) Execute(ctx context.Context, args []string) (string, error) {
	if c.config == nil || c.config.BaseURL == "" {
		return "", fmt.Errorf("vision: no LLM provider configured")
	}

	imagePath, prompt, err := parseArgs(args, c.Usage())
	if err != nil {
		return "", err
	}

	// Validate extension and resolve MIME type.
	ext := filepath.Ext(imagePath)
	mime, ok := mimeFromExt(ext)
	if !ok {
		return "", fmt.Errorf("unsupported image format %q (supported: png, jpg, jpeg, gif, webp, bmp)", ext)
	}

	data, err := readImageFile(imagePath)
	if err != nil {
		return "", err
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mime, b64)

	reqBody := visionRequest{
		Model: c.config.Model,
		Messages: []visionMessage{
			{
				Role: "user",
				Content: []contentPart{
					{Type: "text", Text: prompt},
					{Type: "image_url", ImageURL: &imageURL{URL: dataURI, Detail: "high"}},
				},
			},
		},
		MaxTokens: visionMaxTokens,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	endpoint := strings.TrimSuffix(c.config.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.config.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.config.APIKey)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("vision request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("vision API error (%d): %s", resp.StatusCode, string(respBody))
	}

	var result provider.ChatCompletionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("vision API error: [%s] %s", result.Error.Type, result.Error.Message)
	}
	if len(result.Choices) == 0 || result.Choices[0].Message.Content == nil {
		return "", fmt.Errorf("vision API returned empty response")
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Image: %s (%s, %d bytes)\n", filepath.Base(imagePath), mime, len(data)))
	sb.WriteString(fmt.Sprintf("Prompt: %s\n", prompt))
	sb.WriteString("---\n")
	sb.WriteString(*result.Choices[0].Message.Content)
	return sb.String(), nil
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

func parseArgs(args []string, usage string) (imagePath, prompt string, err error) {
	var positionalPrompt []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--prompt":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("vision: --prompt requires a value")
			}
			// #nosec G602 -- i+1 is checked immediately above.
			value := args[i+1]
			i++
			prompt = value
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("vision: unknown flag: %s", args[i])
			}
			if imagePath == "" {
				imagePath = args[i]
			} else {
				positionalPrompt = append(positionalPrompt, args[i])
			}
		}
	}

	if imagePath == "" {
		return "", "", fmt.Errorf("vision: image_path is required\n\n%s", usage)
	}
	if prompt == "" && len(positionalPrompt) > 0 {
		prompt = strings.Join(positionalPrompt, " ")
	}
	if prompt == "" {
		return "", "", fmt.Errorf("vision: prompt is required\n\n%s", usage)
	}
	return imagePath, prompt, nil
}

// ---------------------------------------------------------------------------
// Internal types — multimodal message format (OpenAI-compatible vision API)
// ---------------------------------------------------------------------------

type visionMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type     string    `json:"type"`
	Text     string    `json:"text,omitempty"`
	ImageURL *imageURL `json:"image_url,omitempty"`
}

type imageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type visionRequest struct {
	Model     string          `json:"model"`
	Messages  []visionMessage `json:"messages"`
	MaxTokens int             `json:"max_tokens"`
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func mimeFromExt(ext string) (string, bool) {
	switch strings.ToLower(ext) {
	case ".png":
		return "image/png", true
	case ".jpg", ".jpeg":
		return "image/jpeg", true
	case ".gif":
		return "image/gif", true
	case ".webp":
		return "image/webp", true
	case ".bmp":
		return "image/bmp", true
	default:
		return "", false
	}
}

func readImageFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot access image: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat image: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("image must be a regular file")
	}
	if info.Size() > maxImageSize {
		return nil, fmt.Errorf("image too large: %d bytes (max %d)", info.Size(), maxImageSize)
	}

	data, err := io.ReadAll(io.LimitReader(f, int64(maxImageSize)+1))
	if err != nil {
		return nil, fmt.Errorf("read image: %w", err)
	}
	if len(data) > maxImageSize {
		return nil, fmt.Errorf("image too large: %d bytes (max %d)", len(data), maxImageSize)
	}
	return data, nil
}
