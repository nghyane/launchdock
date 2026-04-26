package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type responsesStreamCollector struct {
	text          strings.Builder
	messageID     string
	messageIndex  int
	hasMessage    bool
	functionCalls map[string]*responsesFunctionCall
}

type responsesFunctionCall struct {
	ID        string
	CallID    string
	Name      string
	Arguments strings.Builder
	Index     int
}

func (c *responsesStreamCollector) consume(ev SSEEvent) (json.RawMessage, error) {
	if ev.Data == "" || ev.Data == "[DONE]" {
		return nil, nil
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(ev.Data), &obj); err != nil {
		return nil, nil
	}
	typ, _ := obj["type"].(string)

	switch typ {
	case "response.output_item.added":
		c.consumeOutputItemAdded(obj)
	case "response.output_text.delta":
		delta, _ := obj["delta"].(string)
		if delta != "" {
			c.text.WriteString(delta)
			c.noteMessage(obj)
		}
	case "response.output_text.done":
		text, _ := obj["text"].(string)
		if text != "" {
			c.text.Reset()
			c.text.WriteString(text)
			c.noteMessage(obj)
		}
	case "response.function_call_arguments.delta":
		c.consumeFunctionArgumentsDelta(obj)
	case "response.function_call_arguments.done":
		c.consumeFunctionArgumentsDone(obj)
	case "response.output_item.done":
		c.consumeOutputItemDone(obj)
	case "response.completed", "response.done":
		resp, _ := obj["response"].(map[string]any)
		if resp == nil {
			return nil, nil
		}
		c.patchFinalResponse(resp)
		b, err := json.Marshal(resp)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	return nil, nil
}

func (c *responsesStreamCollector) noteMessage(obj map[string]any) {
	c.hasMessage = true
	if id, _ := obj["item_id"].(string); id != "" {
		c.messageID = id
	}
	if idx, ok := numberToInt(obj["output_index"]); ok {
		c.messageIndex = idx
	}
}

func (c *responsesStreamCollector) consumeOutputItemAdded(obj map[string]any) {
	item, _ := obj["item"].(map[string]any)
	if item == nil {
		return
	}
	idx, _ := numberToInt(obj["output_index"])
	switch item["type"] {
	case "message":
		c.hasMessage = true
		c.messageIndex = idx
		if id, _ := item["id"].(string); id != "" {
			c.messageID = id
		}
	case "function_call":
		fc := c.functionCall(item, idx)
		if args, _ := item["arguments"].(string); args != "" {
			fc.Arguments.Reset()
			fc.Arguments.WriteString(args)
		}
	}
}

func (c *responsesStreamCollector) consumeFunctionArgumentsDelta(obj map[string]any) {
	id, _ := obj["item_id"].(string)
	if id == "" {
		return
	}
	idx, _ := numberToInt(obj["output_index"])
	fc := c.functionCall(map[string]any{"id": id, "call_id": id}, idx)
	if delta, _ := obj["delta"].(string); delta != "" {
		fc.Arguments.WriteString(delta)
	}
}

func (c *responsesStreamCollector) consumeFunctionArgumentsDone(obj map[string]any) {
	id, _ := obj["item_id"].(string)
	if id == "" {
		return
	}
	idx, _ := numberToInt(obj["output_index"])
	fc := c.functionCall(map[string]any{"id": id, "call_id": id}, idx)
	if args, _ := obj["arguments"].(string); args != "" {
		fc.Arguments.Reset()
		fc.Arguments.WriteString(args)
	}
}

func (c *responsesStreamCollector) consumeOutputItemDone(obj map[string]any) {
	item, _ := obj["item"].(map[string]any)
	if item == nil {
		return
	}
	idx, _ := numberToInt(obj["output_index"])
	if item["type"] == "function_call" {
		fc := c.functionCall(item, idx)
		if args, _ := item["arguments"].(string); args != "" {
			fc.Arguments.Reset()
			fc.Arguments.WriteString(args)
		}
	}
}

func (c *responsesStreamCollector) functionCall(item map[string]any, idx int) *responsesFunctionCall {
	if c.functionCalls == nil {
		c.functionCalls = map[string]*responsesFunctionCall{}
	}
	id, _ := item["id"].(string)
	callID, _ := item["call_id"].(string)
	key := id
	if key == "" {
		key = callID
	}
	if key == "" {
		key = fmt.Sprintf("call_%d", len(c.functionCalls))
	}
	fc := c.functionCalls[key]
	if fc == nil {
		fc = &responsesFunctionCall{ID: id, CallID: callID, Index: idx}
		c.functionCalls[key] = fc
	}
	if id != "" {
		fc.ID = id
	}
	if callID != "" {
		fc.CallID = callID
	}
	if name, _ := item["name"].(string); name != "" {
		fc.Name = name
	}
	fc.Index = idx
	return fc
}

func (c *responsesStreamCollector) patchFinalResponse(resp map[string]any) {
	if output, ok := resp["output"].([]any); ok && len(output) > 0 {
		return
	}

	var output []any
	if c.hasMessage || c.text.Len() > 0 {
		id := c.messageID
		if id == "" {
			id = "msg_0"
		}
		text := c.text.String()
		output = append(output, map[string]any{
			"id":     id,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{map[string]any{
				"type":        "output_text",
				"text":        text,
				"annotations": []any{},
			}},
		})
		if _, ok := resp["output_text"]; !ok {
			resp["output_text"] = text
		}
	}

	for _, fc := range c.functionCalls {
		id := fc.ID
		if id == "" {
			id = fc.CallID
		}
		callID := fc.CallID
		if callID == "" {
			callID = id
		}
		output = append(output, map[string]any{
			"id":        id,
			"type":      "function_call",
			"call_id":   callID,
			"name":      fc.Name,
			"arguments": fc.Arguments.String(),
			"status":    "completed",
		})
	}

	if output != nil {
		resp["output"] = output
	}
}

func numberToInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func collectResponsesCompletedJSON(r io.Reader) ([]byte, error) {
	collector := &responsesStreamCollector{}
	var finalResponse json.RawMessage
	err := ReadSSE(r, func(ev SSEEvent) error {
		resp, err := collector.consume(ev)
		if err != nil {
			return err
		}
		if len(resp) > 0 {
			finalResponse = append(finalResponse[:0], resp...)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(finalResponse) == 0 {
		return nil, fmt.Errorf("missing response.completed event")
	}
	return finalResponse, nil
}
