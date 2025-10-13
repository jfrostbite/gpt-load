package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	app_errors "gpt-load/internal/errors"
	"gpt-load/internal/models"
	"gpt-load/internal/utils"

	"github.com/gin-gonic/gin"
)

func init() {
	Register("openai-responses", newOpenAIResponsesChannel)
}

type OpenAIResponsesChannel struct {
	*BaseChannel
}

func newOpenAIResponsesChannel(f *Factory, group *models.Group) (ChannelProxy, error) {
	base, err := f.newBaseChannel("openai-responses", group)
	if err != nil {
		return nil, err
	}

	return &OpenAIResponsesChannel{
		BaseChannel: base,
	}, nil
}

func (ch *OpenAIResponsesChannel) ModifyRequest(req *http.Request, apiKey *models.APIKey, group *models.Group) {
	req.Header.Set("Authorization", "Bearer "+apiKey.KeyValue)
}

func (ch *OpenAIResponsesChannel) IsStreamRequest(c *gin.Context, bodyBytes []byte) bool {
	if strings.Contains(c.GetHeader("Accept"), "text/event-stream") {
		return true
	}

	if c.Query("stream") == "true" {
		return true
	}

	var payload map[string]any
	if err := json.Unmarshal(bodyBytes, &payload); err == nil {
		if streamVal, ok := payload["stream"]; ok && streamVal != nil {
			switch v := streamVal.(type) {
			case bool:
				return v
			case string:
				return strings.EqualFold(v, "true")
			case map[string]any:
				return len(v) > 0
			case []any:
				return len(v) > 0
			case float64:
				return v != 0
			default:
				return true
			}
		}
	}

	return false
}

func (ch *OpenAIResponsesChannel) ExtractModel(c *gin.Context, bodyBytes []byte) string {
	type modelPayload struct {
		Model string `json:"model"`
	}
	var p modelPayload
	if err := json.Unmarshal(bodyBytes, &p); err == nil {
		return p.Model
	}
	return ""
}

func (ch *OpenAIResponsesChannel) ValidateKey(ctx context.Context, apiKey *models.APIKey, group *models.Group) (bool, error) {
	upstreamURL := ch.getUpstreamURL()
	if upstreamURL == nil {
		return false, fmt.Errorf("no upstream URL configured for channel %s", ch.Name)
	}

	var reqURL string
	var err error

	// Special case: if ValidationEndpoint is "#", use upstream URL directly
	if ch.ValidationEndpoint == "#" {
		reqURL = upstreamURL.String()
	} else {
		validationEndpoint := ch.ValidationEndpoint
		if validationEndpoint == "" {
			validationEndpoint = "/v1/responses"
		}
		reqURL, err = url.JoinPath(upstreamURL.String(), validationEndpoint)
		if err != nil {
			return false, fmt.Errorf("failed to join upstream URL and validation endpoint: %w", err)
		}
	}

	payload := gin.H{
		"model": ch.TestModel,
		"input": "hi",
	}
	ch.applyParamOverridesForValidation(payload, group)
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("failed to marshal validation payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create validation request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey.KeyValue)
	req.Header.Set("Content-Type", "application/json")
	if ua := strings.TrimSpace(group.EffectiveConfig.UpstreamUserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	if len(group.HeaderRuleList) > 0 {
		headerCtx := utils.NewHeaderVariableContext(group, apiKey)
		utils.ApplyHeaderRules(req, group.HeaderRuleList, headerCtx)
	}

	resp, err := ch.HTTPClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send validation request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}

	errorBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("key is invalid (status %d), but failed to read error body: %w", resp.StatusCode, err)
	}

	parsedError := app_errors.ParseUpstreamError(errorBody)

	return false, fmt.Errorf("[status %d] %s", resp.StatusCode, parsedError)
}
