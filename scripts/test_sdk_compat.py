#!/usr/bin/env python3

import json
import sys
import urllib.error
import urllib.request

from openai import OpenAI


BASE = "http://localhost:8090"
client = OpenAI(base_url=f"{BASE}/v1", api_key="launchdock")


results = []


def record(surface, model, case, ok, note=""):
    results.append({
        "surface": surface,
        "model": model,
        "case": case,
        "result": "pass" if ok else "fail",
        "note": note,
    })


def call_json(path, payload, extra_headers=None):
    headers = {
        "Content-Type": "application/json",
        "Authorization": "Bearer launchdock",
    }
    if extra_headers:
        headers.update(extra_headers)
    req = urllib.request.Request(f"{BASE}{path}", data=json.dumps(payload).encode(), headers=headers)
    with urllib.request.urlopen(req, timeout=180) as r:
        if payload.get("stream"):
            return r.read().decode()
        return json.load(r)


def parse_sse_json(raw):
    events = []
    for block in raw.split("\n\n"):
        if not block.strip():
            continue
        data_lines = []
        for line in block.splitlines():
            if line.startswith("data: "):
                data_lines.append(line[6:])
        if not data_lines:
            continue
        data = "\n".join(data_lines)
        if data == "[DONE]":
            continue
        try:
            events.append(json.loads(data))
        except Exception:
            pass
    return events


def test_chat_basic(model, marker):
    resp = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": f"Reply with exactly: {marker}"}],
        max_tokens=32,
    )
    ok = resp.choices[0].message.content.strip() == marker
    record("chat/completions", model, "basic", ok, resp.choices[0].message.content)


def test_chat_multiturn(model):
    messages = [
        {"role": "user", "content": "My name is Nghia. Reply with exactly: noted."},
    ]
    first = client.chat.completions.create(model=model, messages=messages, max_tokens=32)
    messages.append({"role": "assistant", "content": first.choices[0].message.content})
    messages.append({"role": "user", "content": "What is my name? Reply with exactly one word."})
    second = client.chat.completions.create(model=model, messages=messages, max_tokens=32)
    ok = "nghia" in second.choices[0].message.content.lower()
    record("chat/completions", model, "multi-turn", ok, second.choices[0].message.content)


def test_chat_stream(model, marker):
    raw = call_json(
        "/v1/chat/completions",
        {
            "model": model,
            "messages": [{"role": "user", "content": f"Reply with exactly: {marker}"}],
            "stream": True,
            "max_tokens": 32,
        },
    )
    events = parse_sse_json(raw)
    text = ""
    for ev in events:
        for choice in ev.get("choices", []):
            delta = choice.get("delta") or {}
            if isinstance(delta.get("content"), str):
                text += delta["content"]
    ok = marker in text
    record("chat/completions", model, "stream", ok, text)


TOOLS = [{
    "type": "function",
    "function": {
        "name": "get_weather",
        "description": "Get weather for a city",
        "parameters": {
            "type": "object",
            "properties": {"city": {"type": "string"}},
            "required": ["city"],
        },
    },
}]


def test_chat_tool_forced(model):
    resp = client.chat.completions.create(
        model=model,
        messages=[{"role": "user", "content": "Use get_weather for Hanoi."}],
        tools=TOOLS,
        tool_choice={"type": "function", "function": {"name": "get_weather"}},
        max_tokens=128,
    )
    calls = resp.choices[0].message.tool_calls or []
    ok = len(calls) > 0 and calls[0].function.name == "get_weather"
    note = calls[0].function.arguments if calls else ""
    record("chat/completions", model, "tool-call", ok, note)
    return calls[0] if calls else None


def test_chat_tool_roundtrip(model):
    call = test_chat_tool_forced(model)
    if not call:
        record("chat/completions", model, "tool-roundtrip", False, "no tool call")
        return
    second = client.chat.completions.create(
        model=model,
        messages=[
            {"role": "user", "content": "Use get_weather for Hanoi. After I send the tool result, answer in one sentence."},
            {"role": "assistant", "content": None, "tool_calls": [{"id": call.id, "type": "function", "function": {"name": call.function.name, "arguments": call.function.arguments}}]},
            {"role": "tool", "tool_call_id": call.id, "content": "Sunny 30C"},
        ],
        max_tokens=128,
    )
    content = second.choices[0].message.content or ""
    ok = "30" in content or "sunny" in content.lower()
    record("chat/completions", model, "tool-roundtrip", ok, content)


def test_responses_nonstream():
    body = call_json("/v1/responses", {
        "model": "gpt-5.4",
        "stream": False,
        "input": [{"role": "user", "content": [{"type": "input_text", "text": "Reply with exactly: responses-ok"}]}],
    })
    text = body["output"][0]["content"][0]["text"]
    record("responses", "gpt-5.4", "non-stream", text == "responses-ok", text)


def test_responses_tool_nonstream():
    body = call_json("/v1/responses", {
        "model": "gpt-5.4",
        "stream": False,
        "input": [{"role": "user", "content": [{"type": "input_text", "text": "Use get_weather for Hanoi."}]}],
        "tools": [{
            "type": "function",
            "name": "get_weather",
            "description": "Get weather",
            "parameters": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]},
        }],
        "tool_choice": {"type": "function", "name": "get_weather"},
    })
    output = body.get("output", [])
    ok = output and output[0].get("type") == "function_call"
    record("responses", "gpt-5.4", "tool-call", ok, json.dumps(output[:1]))


def test_messages_basic():
    body = call_json("/v1/messages", {
        "model": "claude-sonnet-4-6",
        "max_tokens": 64,
        "messages": [{"role": "user", "content": "Reply with exactly: messages-ok"}],
    }, {"anthropic-version": "2023-06-01"})
    text = body["content"][0]["text"]
    record("messages", "claude-sonnet-4-6", "basic", text == "messages-ok", text)


def test_messages_tool_roundtrip():
    first = call_json("/v1/messages", {
        "model": "claude-sonnet-4-6",
        "max_tokens": 128,
        "messages": [{"role": "user", "content": "Use get_weather for Hanoi. After I send the tool result, answer in one sentence."}],
        "tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}],
        "tool_choice": {"type": "tool", "name": "get_weather"},
    }, {"anthropic-version": "2023-06-01"})
    call = first["content"][0]
    second = call_json("/v1/messages", {
        "model": "claude-sonnet-4-6",
        "max_tokens": 128,
        "messages": [
            {"role": "user", "content": "Use get_weather for Hanoi. After I send the tool result, answer in one sentence."},
            {"role": "assistant", "content": [call]},
            {"role": "user", "content": [{"type": "tool_result", "tool_use_id": call["id"], "content": "Sunny 30C"}]},
        ],
        "tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object", "properties": {"city": {"type": "string"}}, "required": ["city"]}}],
    }, {"anthropic-version": "2023-06-01"})
    text = second["content"][0]["text"]
    ok = "30" in text or "sunny" in text.lower()
    record("messages", "claude-sonnet-4-6", "tool-roundtrip", ok, text)


def test_messages_thinking_stream():
    raw = call_json("/v1/chat/completions", {
        "model": "claude-sonnet-4-6",
        "messages": [{"role": "user", "content": "Think briefly then reply with exactly: thinking-ok"}],
        "stream": True,
        "max_tokens": 128,
        "thinking": {"type": "enabled", "budget_tokens": 1024},
    })
    events = parse_sse_json(raw)
    saw_thinking = False
    saw_final = False
    for ev in events:
        for choice in ev.get("choices", []):
            delta = choice.get("delta") or {}
            if delta.get("role") == "thinking":
                saw_thinking = True
            if delta.get("content") == "thinking-ok":
                saw_final = True
    ok = saw_thinking and saw_final
    record("chat/completions", "claude-sonnet-4-6", "thinking-stream", ok, "thinking=%s final=%s" % (saw_thinking, saw_final))


def main():
    test_chat_basic("claude-sonnet-4-6", "claude-basic-ok")
    test_chat_basic("gpt-5.4", "gpt-basic-ok")
    test_chat_multiturn("claude-sonnet-4-6")
    test_chat_multiturn("gpt-5.4")
    test_chat_stream("claude-sonnet-4-6", "claude-stream-ok")
    test_chat_stream("gpt-5.4", "gpt-stream-ok")
    test_chat_tool_forced("claude-sonnet-4-6")
    test_chat_tool_forced("gpt-5.4")
    test_chat_tool_roundtrip("claude-sonnet-4-6")
    test_chat_tool_roundtrip("gpt-5.4")
    test_responses_nonstream()
    test_responses_tool_nonstream()
    test_messages_basic()
    test_messages_tool_roundtrip()
    test_messages_thinking_stream()

    print("| Surface | Model | Case | Result | Notes |")
    print("|---|---|---|---|---|")
    failed = 0
    for row in results:
        note = row['note'].replace('\n', ' ')[:80]
        print(f"| {row['surface']} | {row['model']} | {row['case']} | {row['result']} | {note} |")
        if row['result'] != 'pass':
            failed += 1
    sys.exit(1 if failed else 0)


if __name__ == '__main__':
    main()
