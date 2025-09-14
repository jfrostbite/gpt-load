package proxy

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gpt-load/internal/models"
)

type StreamAdapter interface {
	Adapt(c *gin.Context, resp *http.Response, flusher http.Flusher)
}

func (ps *ProxyServer) selectStreamAdapter(group *models.Group) StreamAdapter {
	if group == nil {
		return nil
	}
	name := group.EffectiveConfig.StreamAdapter
	if name == "" && group.EffectiveConfig.StreamAdapterAnthropic {
		name = "anthropic"
	}
	switch strings.ToLower(name) {
	case "anthropic", "anthropicstreamadapter":
		return &anthropicStreamAdapter{}
	case "openai", "openaistreamadapter":
		return &openaiStreamAdapter{}
	default:
		return nil
	}
}
