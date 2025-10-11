package proxy

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
)

func isDeepEmptyText(s string) bool {
	r := strings.NewReplacer(
		"\u00A0", "",
		"\u200B", "",
		"\u200C", "",
		"\u200D", "",
		"\u2060", "",
		"\uFEFF", "",
		"\u180E", "",
		"\u202F", "",
	)
	s2 := r.Replace(s)
	s2 = strings.TrimSpace(s2)
	return s2 == ""
}

func (ps *ProxyServer) applyParamOverrides(bodyBytes []byte, group *models.Group) ([]byte, error) {
	if len(bodyBytes) == 0 {
		return bodyBytes, nil
	}

	var requestData map[string]any
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		logrus.Warnf("failed to unmarshal request body for param override, passing through: %v", err)
		return bodyBytes, nil
	}

	// Step 1: Apply parameter removal first
	if rp := group.EffectiveConfig.RemoveParams; rp != "" {
		params := rp
		seps := []string{",", ";", " ", "|", "/", "\n", "\t"}
		for _, sep := range seps {
			params = strings.ReplaceAll(params, sep, ",")
		}
		for _, key := range strings.Split(params, ",") {
			k := strings.TrimSpace(key)
			if k == "" {
				continue
			}
			delete(requestData, k)
		}
	}

	// Step 2: Apply parameter key replacements
	if replacements := group.EffectiveConfig.ParamKeyReplacements; replacements != "" {
		ps.applyParamKeyReplacements(requestData, replacements)
	}

	// Step 3: Apply parameter overrides with peer-level key checking
	if len(group.ParamOverrides) > 0 {
		for key, value := range group.ParamOverrides {
			if key == "tools" {
				continue
			}
			// Apply peer-level key check if enabled
			if group.EffectiveConfig.PeerLevelKeyCheck {
				// Only override if key doesn't already exist in the request
				// This check happens after removal and replacements to ensure we don't override
				// keys that should be preserved in the downstream request
				if _, exists := requestData[key]; !exists {
					requestData[key] = value
				}
			} else {
				// If peer-level checking is disabled, always override
				requestData[key] = value
			}
		}
		if group.EffectiveConfig.ToolsOverride {
			if tv, ok := group.ParamOverrides["tools"]; ok {
				var existing []any
				if et, ok2 := requestData["tools"].([]any); ok2 {
					existing = et
				}
				overrideArr, ok2 := tv.([]any)
				if !ok2 {
					if m, ok3 := tv.(map[string]any); ok3 {
						overrideArr = []any{m}
					}
				}
				if len(overrideArr) > 0 {
					byName := make(map[string]int)
					for i := range existing {
						if em, ok4 := existing[i].(map[string]any); ok4 {
							name := ""
							if n, ok5 := em["name"].(string); ok5 && n != "" {
								name = n
							}
							if fn, ok5 := em["function"].(map[string]any); ok5 {
								if n2, ok6 := fn["name"].(string); ok6 && n2 != "" {
									name = n2
								}
							}
							if name != "" {
								byName[name] = i
							}
						}
					}
					for _, ov := range overrideArr {
						om, ok7 := ov.(map[string]any)
						if !ok7 {
							continue
						}
						name := ""
						if n, ok8 := om["name"].(string); ok8 && n != "" {
							name = n
						}
						if fn, ok8 := om["function"].(map[string]any); ok8 {
							if n2, ok9 := fn["name"].(string); ok9 && n2 != "" {
								name = n2
							}
						}
						if name == "" {
							continue
						}
						if _, exists := byName[name]; exists {
							continue
						}
						existing = append(existing, om)
						byName[name] = len(existing) - 1
					}
					requestData["tools"] = existing
				}
			}
		}
	}

	// Step 4: Apply max_tokens configuration if set
	if group.EffectiveConfig.MaxTokens > 0 {
		if group.EffectiveConfig.UseOpenAICompat {
			// Check if max_completion_tokens already exists
			if _, exists := requestData["max_completion_tokens"]; !exists {
				requestData["max_completion_tokens"] = group.EffectiveConfig.MaxTokens
			}
		} else {
			// Check if max_tokens already exists
			if _, exists := requestData["max_tokens"]; !exists {
				requestData["max_tokens"] = group.EffectiveConfig.MaxTokens
			}
		}
	}

	// Step 5: Apply force streaming if enabled
	if group.EffectiveConfig.ForceStreaming {
		requestData["stream"] = true
	}

	// Step 6: Apply multimodal transformations
	if group.EffectiveConfig.MultimodalOnly {
		if content, ok := requestData["content"].(string); ok && content != "" {
			requestData["content"] = []map[string]any{{"type": "text", "text": content}}
		}
		if messages, ok := requestData["messages"].([]any); ok {
			for i := range messages {
				if m, ok := messages[i].(map[string]any); ok {
					if contentStr, ok := m["content"].(string); ok {
						m["content"] = []map[string]any{{"type": "text", "text": contentStr}}
					}
				}
			}
		}
	}

	// Step 7: Remove empty text in multimodal messages
	if group.EffectiveConfig.RemoveEmptyTextInMultimodal {
		if msgs, ok := requestData["messages"].([]any); ok {
			for i := range msgs {
				mm, ok := msgs[i].(map[string]any)
				if !ok {
					continue
				}
				if contents, ok2 := mm["content"].([]any); ok2 {
					var cleaned []any
					for _, it := range contents {
						if m2, ok3 := it.(map[string]any); ok3 {
							if tp, _ := m2["type"].(string); tp == "text" {
								if txt, _ := m2["text"].(string); isDeepEmptyText(txt) {
									continue
								}
							}
						}
						cleaned = append(cleaned, it)
					}
					mm["content"] = cleaned
				}
			}
		}
		if topContent, ok := requestData["content"].([]any); ok {
			var cleaned []any
			for _, it := range topContent {
				if m2, ok3 := it.(map[string]any); ok3 {
					if tp, _ := m2["type"].(string); tp == "text" {
						if txt, _ := m2["text"].(string); isDeepEmptyText(txt) {
							continue
						}
					}
				}
				cleaned = append(cleaned, it)
			}
			requestData["content"] = cleaned
		}
	}

	return json.Marshal(requestData)
}

// applyParamKeyReplacements applies parameter key replacements to the request data
// Format: "old_key:new_key,old_key2:new_key2" or using separators like ; | / \n \t
func (ps *ProxyServer) applyParamKeyReplacements(requestData map[string]any, replacements string) {
	if replacements == "" {
		return
	}

	// Normalize separators to commas
	rules := replacements
	seps := []string{";", "|", "/", "\n", "\t"}
	for _, sep := range seps {
		rules = strings.ReplaceAll(rules, sep, ",")
	}

	// Process each replacement rule
	for _, rule := range strings.Split(rules, ",") {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}

		// Split by colon to get old_key:new_key
		parts := strings.SplitN(rule, ":", 2)
		if len(parts) != 2 {
			continue
		}

		oldKey := strings.TrimSpace(parts[0])
		newKey := strings.TrimSpace(parts[1])

		if oldKey == "" || newKey == "" || oldKey == newKey {
			continue
		}

		// If old key exists and new key doesn't exist, replace it
		if value, exists := requestData[oldKey]; exists {
			if _, newExists := requestData[newKey]; !newExists {
				requestData[newKey] = value
				delete(requestData, oldKey)
			}
		}
	}
}

// logUpstreamError provides a centralized way to log errors from upstream interactions.
func logUpstreamError(context string, err error) {
	if err == nil {
		return
	}
	if app_errors.IsIgnorableError(err) {
		logrus.Debugf("Ignorable upstream error in %s: %v", context, err)
	} else {
		logrus.Errorf("Upstream error in %s: %v", context, err)
	}
}

// handleGzipCompression checks for gzip encoding and decompresses the body if necessary.
func handleGzipCompression(resp *http.Response, bodyBytes []byte) []byte {
	if resp.Header.Get("Content-Encoding") == "gzip" {
		reader, gzipErr := gzip.NewReader(bytes.NewReader(bodyBytes))
		if gzipErr != nil {
			logrus.Warnf("Failed to create gzip reader for error body: %v", gzipErr)
			return bodyBytes
		}
		defer reader.Close()

		decompressedBody, readAllErr := io.ReadAll(reader)
		if readAllErr != nil {
			logrus.Warnf("Failed to decompress gzip error body: %v", readAllErr)
			return bodyBytes
		}
		return decompressedBody
	}
	return bodyBytes
}