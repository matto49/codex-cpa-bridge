package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ProbeAnthropicMessages sends one explicitly requested, minimal inference
// request. It reports protocol success without including credentials or model
// output in diagnostics.
func ProbeAnthropicMessages(m Manifest, model string) HTTPProbeReport {
	model = strings.TrimSpace(model)
	if model == "" || len(model) > 128 || strings.ContainsAny(model, "\r\n") {
		return HTTPProbeReport{Status: "invalid model", Detail: "specify one CPA model ID"}
	}
	token, authSource := loadAuthToken(m.Profiles.CPA)
	if token == "" {
		return HTTPProbeReport{Status: "credential unavailable", Detail: authSource}
	}
	payload, err := json.Marshal(map[string]any{
		"model": model, "max_tokens": 16,
		"messages": []map[string]string{{"role": "user", "content": "Reply with exactly OK."}},
	})
	if err != nil {
		return HTTPProbeReport{Status: "request error"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.Profiles.CPA.Endpoint, "/")+"/messages", bytes.NewReader(payload))
	if err != nil {
		return HTTPProbeReport{Status: "request error"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", token)
	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return HTTPProbeReport{Status: shortErrorName(err), Detail: "Anthropic request did not complete"}
	}
	defer resp.Body.Close()
	status := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return HTTPProbeReport{Status: status, Detail: "CPA or upstream rejected the Anthropic request"}
	}
	var message struct {
		Type    string `json:"type"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&message); err != nil || message.Type != "message" || message.Model == "" {
		return HTTPProbeReport{Status: status, Detail: "response is not an Anthropic message"}
	}
	for _, block := range message.Content {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			return HTTPProbeReport{OK: true, Status: status, Detail: "Anthropic message response received"}
		}
	}
	return HTTPProbeReport{Status: status, Detail: "Anthropic message has no text content"}
}
