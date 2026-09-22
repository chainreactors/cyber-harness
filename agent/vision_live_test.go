//go:build live_llm

package agent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/aop"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// The random answer exists only in pixels, never in prompts or filenames.
func TestLiveVisionAgent(t *testing.T) {
	cfg := provider.ProviderConfig{Provider: os.Getenv("CYBER_HARNESS_LLM_PROVIDER"), BaseURL: os.Getenv("CYBER_HARNESS_LLM_BASE_URL"), APIKey: os.Getenv("CYBER_HARNESS_LLM_API_KEY"), Model: os.Getenv("CYBER_HARNESS_LLM_MODEL"), MaxTokens: 2048}
	if cfg.APIKey == "" || cfg.Model == "" || cfg.BaseURL == "" {
		t.Fatal("live vision requires CYBER_HARNESS_LLM_API_KEY, _MODEL and _BASE_URL")
	}
	for _, mode := range []string{"inline", "file", "tool"} {
		for _, stream := range []bool{false, true} {
			name := mode + map[bool]string{false: "/completion", true: "/stream"}[stream]
			t.Run(name, func(t *testing.T) {
				seed := make([]byte, 4)
				if _, err := rand.Read(seed); err != nil {
					t.Fatal(err)
				}
				code := strings.ToUpper(hex.EncodeToString(seed))
				small := image.NewRGBA(image.Rect(0, 0, 90, 28))
				draw.Draw(small, small.Bounds(), image.White, image.Point{}, draw.Src)
				d := font.Drawer{Dst: small, Src: image.Black, Face: basicfont.Face7x13, Dot: fixed.P(8, 19)}
				d.DrawString(code)
				large := image.NewRGBA(image.Rect(0, 0, 720, 224))
				for y := 0; y < 224; y++ {
					for x := 0; x < 720; x++ {
						large.Set(x, y, color.RGBAModel.Convert(small.At(x/8, y/8)))
					}
				}
				var encoded bytes.Buffer
				if err := png.Encode(&encoded, large); err != nil {
					t.Fatal(err)
				}
				llm, err := provider.NewProvider(&cfg)
				if err != nil {
					t.Fatal(err)
				}
				a := NewAgent(Config{Loop: StandardLoop{}, Provider: llm, Model: cfg.Model, MaxTokens: 2048, MaxTurns: 2, Stream: stream})
				input := TextInput("Read the hexadecimal code in the image. Reply with only that code. Do not use tools.")
				switch mode {
				case "inline":
					input.Content = append(input.Content, aop.Image("image/png", encoded.Bytes()))
				case "file":
					path := filepath.Join(t.TempDir(), "input.png")
					if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
						t.Fatal(err)
					}
					input.Content = append(input.Content, uriImageMessage(path, "").Content...)
				case "tool":
					a.LoadMessages([]*aop.Message{TextInput("Inspect the screenshot returned by the tool."), {Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: "capture_1", Name: "record", Arguments: &aop.EncodedValue{MediaType: aop.JSONMediaType, Data: []byte(`{"action":"screenshot"}`)}}}}}}, provider.ToolResultMessage("capture_1", &aop.ToolResult{Output: []*aop.Content{aop.Text("Screenshot captured."), aop.Image("image/png", encoded.Bytes())}})})
				}
				ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
				defer cancel()
				result, err := a.Run(ctx, input)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(strings.ToUpper(result.Output), code) {
					t.Fatalf("image code mismatch: want %s, got %q", code, result.Output)
				}
				t.Logf("%s %s: image-only random code read correctly", cfg.Provider, cfg.Model)
			})
		}
	}
}
