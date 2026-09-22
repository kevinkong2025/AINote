package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ChatChunk is a unified streaming event sent to the frontend.
type ChatChunk struct {
	Type             string `json:"type"` // "delta" | "done" | "error"
	Text             string `json:"text,omitempty"`
	Error            string `json:"error,omitempty"`
	PromptTokens     int    `json:"promptTokens,omitempty"`
	CompletionTokens int    `json:"completionTokens,omitempty"`
}

type llmMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func normalizeBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("Base URL 为空")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("Base URL 格式不正确: %s", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

// TestConnectivity checks that the host is reachable.
func TestConnectivity(base string) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %v", err)
	}
	host := u.Host
	if !strings.Contains(host, ":") {
		if u.Scheme == "https" {
			host += ":443"
		} else {
			host += ":80"
		}
	}
	conn, err := net.DialTimeout("tcp", host, 8*time.Second)
	if err != nil {
		return fmt.Errorf("无法连接到 %s: %v", u.Host, err)
	}
	_ = conn.Close()
	return nil
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 120 * time.Second}
}

// ---- OpenAI format ----

func openaiChat(p LLMProfile, msgs []llmMessage, stream bool) (*http.Response, error) {
	body := map[string]any{
		"model":    p.Model,
		"messages": msgs,
		"stream":   stream,
	}
	if stream {
		body["stream_options"] = map[string]any{"include_usage": true}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", p.BaseURL+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return httpClient().Do(req)
}

// ---- Anthropic format ----

func anthropicChat(p LLMProfile, msgs []llmMessage, stream bool) (*http.Response, error) {
	var system string
	var amsgs []map[string]string
	for _, m := range msgs {
		if m.Role == "system" {
			system += m.Content + "\n"
			continue
		}
		amsgs = append(amsgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	maxTok := 4096
	body := map[string]any{
		"model":      p.Model,
		"messages":   amsgs,
		"max_tokens": maxTok,
		"stream":     stream,
	}
	if system != "" {
		body["system"] = strings.TrimSpace(system)
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", p.BaseURL+"/messages", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	return httpClient().Do(req)
}

// SimpleChat sends a non-streaming request and returns the assistant text.
func SimpleChat(p LLMProfile, msgs []llmMessage) (string, error) {
	var resp *http.Response
	var err error
	if p.Format == "anthropic" {
		resp, err = anthropicChat(p, msgs, false)
	} else {
		resp, err = openaiChat(p, msgs, false)
	}
	if err != nil {
		return "", fmt.Errorf("请求发送失败: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("接口返回 %d: %s", resp.StatusCode, truncate(string(b), 500))
	}
	if p.Format == "anthropic" {
		var r struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			return "", fmt.Errorf("响应解析失败: %v", err)
		}
		for _, c := range r.Content {
			if c.Type == "text" {
				return c.Text, nil
			}
		}
		return "", fmt.Errorf("响应中没有文本内容")
	}
	var r struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return "", fmt.Errorf("响应解析失败: %v", err)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("响应中没有 choices")
	}
	return r.Choices[0].Message.Content, nil
}

// StreamChat streams a chat request, emitting unified chunks. Returns final token usage.
func StreamChat(p LLMProfile, msgs []llmMessage, emit func(ChatChunk)) error {
	var resp *http.Response
	var err error
	if p.Format == "anthropic" {
		resp, err = anthropicChat(p, msgs, true)
	} else {
		resp, err = openaiChat(p, msgs, true)
	}
	if err != nil {
		return fmt.Errorf("请求发送失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("接口返回 %d: %s", resp.StatusCode, truncate(string(b), 500))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var usage struct{ prompt, completion int }

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		if p.Format == "anthropic" {
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
					InputTokens  int `json:"input_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			switch ev.Type {
			case "content_block_delta":
				if ev.Delta.Text != "" {
					emit(ChatChunk{Type: "delta", Text: ev.Delta.Text})
				}
			case "message_start":
				usage.prompt = ev.Message.Usage.InputTokens
			case "message_delta":
				if ev.Usage.OutputTokens > 0 {
					usage.completion = ev.Usage.OutputTokens
				}
				if ev.Usage.InputTokens > 0 {
					usage.prompt = ev.Usage.InputTokens
				}
			}
		} else {
			var ev struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &ev); err != nil {
				continue
			}
			for _, c := range ev.Choices {
				if c.Delta.Content != "" {
					emit(ChatChunk{Type: "delta", Text: c.Delta.Content})
				}
			}
			if ev.Usage != nil {
				usage.prompt = ev.Usage.PromptTokens
				usage.completion = ev.Usage.CompletionTokens
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("流读取中断: %v", err)
	}
	emit(ChatChunk{Type: "done", PromptTokens: usage.prompt, CompletionTokens: usage.completion})
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// estimateTokens gives a rough token count when the API does not report usage.
func estimateTokens(texts ...string) int {
	total := 0
	for _, t := range texts {
		runes := []rune(t)
		cjk := 0
		for _, r := range runes {
			if r > 0x2E7F {
				cjk++
			}
		}
		total += cjk + (len(runes)-cjk)/4
	}
	return total
}

// CompactNotes asks the LLM to compress note contents into a dense context brief.
func CompactNotes(p LLMProfile, notes []ArchivedNote) (string, error) {
	var sb strings.Builder
	for _, n := range notes {
		sb.WriteString("## " + n.Name + "\n")
		sb.WriteString(truncate(n.Content, 20000))
		sb.WriteString("\n\n")
	}
	msgs := []llmMessage{
		{Role: "system", Content: "你是笔记压缩助手。把用户提供的多篇笔记压缩成一份精炼的上下文摘要（保留关键技术点、方案、结论和重要细节），供后续对话作为背景知识使用。直接输出摘要正文，不要寒暄。"},
		{Role: "user", Content: sb.String()},
	}
	return SimpleChat(p, msgs)
}
