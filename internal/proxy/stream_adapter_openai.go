package proxy

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type openaiStreamAdapter struct{}

func (a *openaiStreamAdapter) Adapt(c *gin.Context, resp *http.Response, flusher http.Flusher) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	writeSSE := func(obj any) error {
		b, _ := json.Marshal(obj)
		var out bytes.Buffer
		out.WriteString("data: ")
		out.Write(b)
		out.WriteString("\n\n")
		_, err := c.Writer.Write(out.Bytes())
		if err == nil {
			flusher.Flush()
		}
		return err
	}

	writeDone := func() {
		c.Writer.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}

	var id string
	var model string
	var created int64
	var serviceTier any = nil
	var systemFingerprint any = nil

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}

		var m map[string]any
		if err := json.Unmarshal([]byte(payload), &m); err != nil {
			continue
		}

		t, _ := m["type"].(string)
		switch t {
		case "message_start":
			if msg, ok := m["message"].(map[string]any); ok {
				if v, ok := msg["id"].(string); ok {
					id = v
				}
				if v, ok := msg["model"].(string); ok {
					model = v
				}
				if v, ok := msg["created_at"].(float64); ok {
					created = int64(v)
				} else if created == 0 {
					created = time.Now().Unix()
				}
			}
			writeSSE(map[string]any{
				"id":                 firstNonEmpty(id, randomID()),
				"object":             "chat.completion.chunk",
				"created":            created,
				"model":              model,
				"service_tier":       serviceTier,
				"system_fingerprint": systemFingerprint,
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{"role": "assistant", "content": "", "refusal": nil},
						"finish_reason": nil,
					},
				},
				"usage":       nil,
				"obfuscation": randObf(12),
			})
		case "content_block_delta":
			if delta, ok := m["delta"].(map[string]any); ok {
				if tt, _ := delta["type"].(string); tt == "input_text_delta" || tt == "output_text_delta" {
					content, _ := delta["text"].(string)
					writeSSE(map[string]any{
						"id":                 firstNonEmpty(id, randomID()),
						"object":             "chat.completion.chunk",
						"created":            created,
						"model":              model,
						"service_tier":       serviceTier,
						"system_fingerprint": systemFingerprint,
						"choices": []any{
							map[string]any{
								"index": 0,
								"delta": map[string]any{"content": content},
								"finish_reason": nil,
							},
						},
						"usage":       nil,
						"obfuscation": randObf(12),
					})
				}
			}
		case "message_delta":
			writeSSE(map[string]any{
				"id":                 firstNonEmpty(id, randomID()),
				"object":             "chat.completion.chunk",
				"created":            created,
				"model":              model,
				"service_tier":       serviceTier,
				"system_fingerprint": systemFingerprint,
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{},
						"finish_reason": nil,
					},
				},
				"usage":       nil,
				"obfuscation": randObf(8),
			})
		case "message_stop":
			writeSSE(map[string]any{
				"id":                 firstNonEmpty(id, randomID()),
				"object":             "chat.completion.chunk",
				"created":            created,
				"model":              model,
				"service_tier":       serviceTier,
				"system_fingerprint": systemFingerprint,
				"choices": []any{
					map[string]any{
						"index": 0,
						"delta": map[string]any{},
						"finish_reason": "stop",
					},
				},
				"usage":       nil,
				"obfuscation": randObf(8),
			})
			writeSSE(map[string]any{
				"id":                 firstNonEmpty(id, randomID()),
				"object":             "chat.completion.chunk",
				"created":            created,
				"model":              model,
				"service_tier":       serviceTier,
				"system_fingerprint": systemFingerprint,
				"choices":            []any{},
				"usage": map[string]any{
					"prompt_tokens":            0,
					"completion_tokens":         0,
					"total_tokens":              0,
					"prompt_tokens_details":     map[string]any{"cached_tokens": 0, "audio_tokens": 0},
					"completion_tokens_details": map[string]any{"reasoning_tokens": 0, "audio_tokens": 0, "accepted_prediction_tokens": 0, "rejected_prediction_tokens": 0},
				},
				"obfuscation": randObf(8),
			})
		case "ping":
			continue
		}
	}
	writeDone()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func randomID() string {
	b := make([]byte, 6)
	rand.Read(b)
	enc := base64.RawURLEncoding.EncodeToString(b)
	return "chatcmpl-" + enc
}

func randObf(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
