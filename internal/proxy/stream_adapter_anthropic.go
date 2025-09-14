package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type anthropicStreamAdapter struct{}

func (a *anthropicStreamAdapter) Adapt(c *gin.Context, resp *http.Response, flusher http.Flusher) {
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	writeSSE := func(event string, obj any) error {
		b, _ := json.Marshal(obj)
		var out bytes.Buffer
		out.WriteString("event: ")
		out.WriteString(event)
		out.WriteString("\n")
		out.WriteString("data: ")
		out.Write(b)
		out.WriteString("\n\n")
		_, err := c.Writer.Write(out.Bytes())
		if err == nil {
			flusher.Flush()
		}
		return err
	}
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
			writeSSE("message_start", map[string]any{"type": "message_start", "message": m["message"]})
		case "content_block_start":
			writeSSE("content_block_start", map[string]any{"type": "content_block_start", "index": m["index"], "content_block": m["content_block"]})
		case "content_block_delta":
			writeSSE("content_block_delta", map[string]any{"type": "content_block_delta", "index": m["index"], "delta": m["delta"]})
		case "content_block_stop":
			writeSSE("content_block_stop", map[string]any{"type": "content_block_stop", "index": m["index"]})
		case "message_delta":
			writeSSE("message_delta", map[string]any{"type": "message_delta", "delta": m["delta"], "usage": m["usage"]})
		case "message_stop":
			writeSSE("message_stop", map[string]any{"type": "message_stop"})
		case "ping":
			writeSSE("ping", map[string]any{"type": "ping"})
		default:
			continue
		}
	}
}
