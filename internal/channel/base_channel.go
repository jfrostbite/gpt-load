package channel

import (
	"bytes"
	"fmt"
	"gpt-load/internal/models"
	"gpt-load/internal/types"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"

	"gorm.io/datatypes"
)

type UpstreamInfo struct {
	URL           *url.URL
	Weight        int
	CurrentWeight int
}

type BaseChannel struct {
	Name               string
	Upstreams          []UpstreamInfo
	HTTPClient         *http.Client
	StreamClient       *http.Client
	TestModel          string
	ValidationEndpoint string
	upstreamLock       sync.Mutex

	channelType     string
	groupUpstreams  datatypes.JSON
	effectiveConfig *types.SystemSettings
}

func (b *BaseChannel) getUpstreamURL() *url.URL {
	b.upstreamLock.Lock()
	defer b.upstreamLock.Unlock()

	if len(b.Upstreams) == 0 {
		return nil
	}
	if len(b.Upstreams) == 1 {
		return b.Upstreams[0].URL
	}

	totalWeight := 0
	var best *UpstreamInfo

	for i := range b.Upstreams {
		up := &b.Upstreams[i]
		totalWeight += up.Weight
		up.CurrentWeight += up.Weight

		if best == nil || up.CurrentWeight > best.CurrentWeight {
			best = up
		}
	}

	if best == nil {
		return b.Upstreams[0].URL
	}

	best.CurrentWeight -= totalWeight
	return best.URL
}

func (b *BaseChannel) BuildUpstreamURL(originalURL *url.URL, group *models.Group) (string, error) {
	base := b.getUpstreamURL()
	if base == nil {
		return "", fmt.Errorf("no upstream URL configured for channel %s", b.Name)
	}

	finalURL := *base
	proxyPrefix := "/proxy/" + group.Name
	requestPath := originalURL.Path
	requestPath = strings.TrimPrefix(requestPath, proxyPrefix)

	finalURL.Path = strings.TrimRight(finalURL.Path, "/") + requestPath

	finalURL.RawQuery = originalURL.RawQuery

	return finalURL.String(), nil
}

func (b *BaseChannel) IsConfigStale(group *models.Group) bool {
	if b.channelType != group.ChannelType {
		return true
	}
	if b.TestModel != group.TestModel {
		return true
	}
	if b.ValidationEndpoint != group.ValidationEndpoint {
		return true
	}
	if !bytes.Equal(b.groupUpstreams, group.Upstreams) {
		return true
	}
	if !reflect.DeepEqual(b.effectiveConfig, &group.EffectiveConfig) {
		return true
	}
	return false
}

func (b *BaseChannel) GetHTTPClient() *http.Client {
	return b.HTTPClient
}

func (b *BaseChannel) GetStreamClient() *http.Client {
	return b.StreamClient
}

func (b *BaseChannel) applyToolsOverride(payload map[string]any, group *models.Group) {
	if group == nil {
		return
	}
	if !group.EffectiveConfig.ToolsOverride {
		return
	}
	if len(group.ParamOverrides) == 0 {
		return
	}
	tv, ok := group.ParamOverrides["tools"]
	if !ok {
		return
	}
	var existing []any
	if et, ok := payload["tools"].([]any); ok {
		existing = et
	}
	var overrideArr []any
	switch v := tv.(type) {
	case []any:
		overrideArr = v
	case map[string]any:
		overrideArr = []any{v}
	default:
		return
	}
	byName := make(map[string]bool)
	for _, it := range existing {
		if em, ok := it.(map[string]any); ok {
			name := ""
			if n, ok := em["name"].(string); ok && n != "" {
				name = n
			}
			if fn, ok := em["function"].(map[string]any); ok {
				if n2, ok := fn["name"].(string); ok && n2 != "" {
					name = n2
				}
			}
			if name != "" {
				byName[name] = true
			}
		}
	}
	for _, ov := range overrideArr {
		om, ok := ov.(map[string]any)
		if !ok {
			continue
		}
		name := ""
		if n, ok := om["name"].(string); ok && n != "" {
			name = n
		}
		if fn, ok := om["function"].(map[string]any); ok {
			if n2, ok := fn["name"].(string); ok && n2 != "" {
				name = n2
			}
		}
		if name == "" {
			continue
		}
		if byName[name] {
			continue
		}
		existing = append(existing, om)
		byName[name] = true
	}
	if len(existing) > 0 {
		payload["tools"] = existing
	}
}

func (b *BaseChannel) applyParamOverridesForValidation(payload map[string]any, group *models.Group) {
	if group == nil {
		return
	}
	if len(group.ParamOverrides) > 0 {
		for k, v := range group.ParamOverrides {
			if k == "tools" {
				continue
			}
			payload[k] = v
		}
	}
	b.applyToolsOverride(payload, group)

	if group.EffectiveConfig.MultimodalOnly {
		if content, ok := payload["content"].(string); ok && content != "" {
			payload["content"] = []map[string]any{{"type": "text", "text": content}}
		}
		if messages, ok := payload["messages"].([]any); ok {
			for i := range messages {
				if m, ok := messages[i].(map[string]any); ok {
					if contentStr, ok := m["content"].(string); ok {
						m["content"] = []map[string]any{{"type": "text", "text": contentStr}}
					}
				}
			}
		}
	}

	if group.EffectiveConfig.RemoveEmptyTextInMultimodal {
		empty := func(s string) bool {
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
		if msgs, ok := payload["messages"].([]any); ok {
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
								if txt, _ := m2["text"].(string); empty(txt) {
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
		if topContent, ok := payload["content"].([]any); ok {
			var cleaned []any
			for _, it := range topContent {
				if m2, ok3 := it.(map[string]any); ok3 {
					if tp, _ := m2["type"].(string); tp == "text" {
						if txt, _ := m2["text"].(string); empty(txt) {
							continue
						}
					}
				}
				cleaned = append(cleaned, it)
			}
			payload["content"] = cleaned
		}
	}

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
			delete(payload, k)
		}
	}
}
