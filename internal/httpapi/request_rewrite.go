package httpapi

import "encoding/json"

func prepareResponsesOAuthBody(body []byte, forceStream bool) []byte {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		return body
	}
	if _, ok := req["instructions"]; !ok {
		req["instructions"] = "You are a helpful assistant."
	}
	req["store"] = false
	if forceStream {
		req["stream"] = true
	}
	if _, ok := req["tool_choice"]; !ok {
		if tools, ok := req["tools"].([]any); ok && len(tools) > 0 {
			req["tool_choice"] = "auto"
			req["parallel_tool_calls"] = true
		}
	}
	input, ok := req["input"]
	if ok && input != nil {
		switch v := input.(type) {
		case string:
			req["input"] = []map[string]any{{
				"role": "user",
				"content": []map[string]any{{
					"type": "input_text",
					"text": v,
				}},
			}}
		case map[string]any:
			if _, hasRole := v["role"]; hasRole {
				req["input"] = []any{v}
			} else if text, _ := v["text"].(string); text != "" {
				req["input"] = []map[string]any{{
					"role": "user",
					"content": []map[string]any{{
						"type": "input_text",
						"text": text,
					}},
				}}
			}
		}
	}
	out, err := json.Marshal(req)
	if err != nil {
		return body
	}
	return out
}
