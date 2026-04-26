package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCollectResponsesCompletedJSONPatchesEmptyOutputFromDeltas(t *testing.T) {
	stream := "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"output\":[]}}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"output_index\":0,\"content_index\":0,\"delta\":\"hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"status\":\"completed\",\"output\":[],\"usage\":{\"output_tokens\":8}}}\n\n"
	_ = stream

	body, err := collectResponsesCompletedJSON(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}

	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output length = %d, want 1; body=%s", len(output), body)
	}
	msg, _ := output[0].(map[string]any)
	content, _ := msg["content"].([]any)
	part, _ := content[0].(map[string]any)
	if got := part["text"]; got != "hi" {
		t.Fatalf("text = %v, want hi; body=%s", got, body)
	}
	if got := resp["output_text"]; got != "hi" {
		t.Fatalf("output_text = %v, want hi", got)
	}
}

func TestCollectResponsesCompletedJSONKeepsNonEmptyOutput(t *testing.T) {
	stream := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_1\",\"delta\":\"patched\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"original\"}]}]}}\n\n"

	body, err := collectResponsesCompletedJSON(strings.NewReader(stream))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "patched") {
		t.Fatalf("patched non-empty upstream output: %s", body)
	}
	if !strings.Contains(string(body), "original") {
		t.Fatalf("missing original output: %s", body)
	}
}
