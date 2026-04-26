package protocol

import (
	"encoding/json"
	"testing"
)

func TestChatToResponsesRequestConvertsImageURL(t *testing.T) {
	chat := []byte(`{
		"model":"gpt-5.4",
		"messages":[{
			"role":"user",
			"content":[
				{"type":"text","text":"look"},
				{"type":"image_url","image_url":{"url":"data:image/png;base64,abc","detail":"high"}}
			]
		}]
	}`)

	body, err := ChatToResponsesRequest(chat)
	if err != nil {
		t.Fatal(err)
	}

	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	input := req["input"].([]any)
	message := input[0].(map[string]any)
	content := message["content"].([]any)
	image := content[1].(map[string]any)

	if got := image["type"]; got != "input_image" {
		t.Fatalf("image type = %v, want input_image; body=%s", got, body)
	}
	if got := image["image_url"]; got != "data:image/png;base64,abc" {
		t.Fatalf("image_url = %v", got)
	}
	if got := image["detail"]; got != "high" {
		t.Fatalf("detail = %v", got)
	}
}
