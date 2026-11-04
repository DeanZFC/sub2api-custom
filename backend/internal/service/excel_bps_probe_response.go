package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

type bpsAccountProbeResponse struct {
	Status string            `json:"status"`
	Output []json.RawMessage `json:"output"`
}

type bpsAccountProbeItem struct {
	Type                  string    `json:"type"`
	Role                  string    `json:"role"`
	Name                  string    `json:"name"`
	Namespace             string    `json:"namespace"`
	CallID                string    `json:"call_id"`
	Arguments             string    `json:"arguments"`
	EncryptedFunctionArgs *[]string `json:"encrypted_function_args"`
	Content               []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
}

func bpsProbeUserMessage(text string) map[string]any {
	return map[string]any{"type": "message", "role": "user", "content": []any{
		map[string]any{"type": "input_text", "text": text},
	}}
}

func parseBPSAccountProbeResponse(wire []byte) (bpsAccountProbeResponse, error) {
	scanner := bufio.NewScanner(bytes.NewReader(wire))
	scanner.Buffer(make([]byte, 0, 4096), 1<<20)
	var completed *bpsAccountProbeResponse
	for scanner.Scan() {
		line := scanner.Bytes()
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(line[len("data:"):])
		if bytes.Equal(data, []byte("[DONE]")) {
			continue
		}
		var event struct {
			Type     string          `json:"type"`
			Response json.RawMessage `json:"response"`
		}
		if err := json.Unmarshal(data, &event); err != nil {
			return bpsAccountProbeResponse{}, err
		}
		if event.Type == "response.failed" || event.Type == "response.incomplete" {
			return bpsAccountProbeResponse{}, errors.New("bps response failed")
		}
		if event.Type != "response.completed" {
			continue
		}
		if completed != nil {
			return bpsAccountProbeResponse{}, errors.New("duplicate BPS completion")
		}
		var response bpsAccountProbeResponse
		if err := json.Unmarshal(event.Response, &response); err != nil || response.Status != "completed" {
			return bpsAccountProbeResponse{}, errors.New("invalid BPS completion")
		}
		completed = &response
	}
	if err := scanner.Err(); err != nil {
		return bpsAccountProbeResponse{}, err
	}
	if completed == nil {
		return bpsAccountProbeResponse{}, errors.New("missing BPS completion")
	}
	return *completed, nil
}

func bpsProbeExactText(response bpsAccountProbeResponse, nonce string) bool {
	var text strings.Builder
	sawText := false
	for _, raw := range response.Output {
		var item bpsAccountProbeItem
		if json.Unmarshal(raw, &item) != nil {
			return false
		}
		if item.Type == "reasoning" {
			continue
		}
		if item.Type != "message" || item.Role != "assistant" {
			return false
		}
		for _, content := range item.Content {
			if content.Type != "output_text" {
				return false
			}
			sawText = true
			_, _ = text.WriteString(content.Text)
		}
	}
	return sawText && strings.TrimSpace(text.String()) == nonce
}
