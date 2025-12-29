package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const catReply = "喵喵喵喵~"

var (
	enableToolCalls bool
	logBodies       bool
	logMaxBytes     int
)

type messageRequest struct {
	Model     string `json:"model"`
	Messages  any    `json:"messages"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
	Tools     any    `json:"tools"`
}

type completionRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	Stream bool   `json:"stream"`
}

type contentBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Input any    `json:"input,omitempty"`
}

type messageResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        usage          `json:"usage"`
}

type usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type messageStartEvent struct {
	Type    string          `json:"type"`
	Message messageStartMsg `json:"message"`
}

type messageStartMsg struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []contentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   *string        `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        usage          `json:"usage"`
}

type contentBlockStartEvent struct {
	Type         string       `json:"type"`
	Index        int          `json:"index"`
	ContentBlock contentBlock `json:"content_block"`
}

type contentBlockDeltaEvent struct {
	Type  string       `json:"type"`
	Index int          `json:"index"`
	Delta contentDelta `json:"delta"`
}

type contentDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type contentBlockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type messageDeltaEvent struct {
	Type  string       `json:"type"`
	Delta messageDelta `json:"delta"`
	Usage usage        `json:"usage"`
}

type messageDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type messageStopEvent struct {
	Type string `json:"type"`
}

type completionResponse struct {
	Completion string `json:"completion"`
	StopReason string `json:"stop_reason"`
	Stop       string `json:"stop"`
	LogID      string `json:"log_id"`
}

func main() {
	enableToolCalls = envBoolDefault("MOCK_TOOL_CALLS", false)
	logBodies = envBoolDefault("HONEYPOT_LOG_BODY", true)
	logMaxBytes = envIntDefault("HONEYPOT_LOG_MAX_BYTES", 1024*1024)

	if logPath := os.Getenv("HONEYPOT_LOG_PATH"); logPath != "" {
		logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			log.Fatalf("open log file: %v", err)
		}
		log.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", handleMessages)
	mux.HandleFunc("/v1/complete", handleComplete)
	mux.HandleFunc("/v1/models", handleModels)
	mux.HandleFunc("/api/event_logging/batch", handleEventLogging)
	mux.HandleFunc("/", handleRoot)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("Anthropic mock listening on %s", addr)
	if err := http.ListenAndServe(addr, logging(mux)); err != nil {
		log.Fatal(err)
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok","message":"anthropic mock: replies with cat sounds"}`))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"data":[{"id":"claude-mock","type":"model"}]}`))
}

func handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logRequestBody(r)

	var req messageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = "claude-mock"
	}

	toolUses := pickToolUses(req)
	includeText := len(toolUses) == 0
	stopReason := "end_turn"
	if len(toolUses) > 0 {
		stopReason = "tool_use"
	}

	if req.Stream {
		streamMessage(w, model, includeText, toolUses, stopReason)
		return
	}

	var content []contentBlock
	if includeText {
		content = append(content, contentBlock{Type: "text", Text: catReply})
	}
	if len(toolUses) > 0 {
		content = append(content, toolUses...)
	}

	resp := messageResponse{
		ID:         newID("msg"),
		Type:       "message",
		Role:       "assistant",
		Content:    content,
		Model:      model,
		StopReason: stopReason,
		Usage: usage{
			InputTokens:  0,
			OutputTokens: 0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}

func streamMessage(w http.ResponseWriter, model string, includeText bool, toolUses []contentBlock, stopReason string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	start := messageStartEvent{
		Type: "message_start",
		Message: messageStartMsg{
			ID:      newID("msg"),
			Type:    "message",
			Role:    "assistant",
			Content: []contentBlock{},
			Model:   model,
			Usage: usage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		},
	}
	writeSSE(w, "message_start", start)

	index := 0
	if includeText {
		streamTextBlock(w, index, catReply)
		index++
	}
	for _, toolUse := range toolUses {
		streamToolUseBlock(w, index, toolUse)
		index++
	}

	msgDelta := messageDeltaEvent{
		Type: "message_delta",
		Delta: messageDelta{
			StopReason: stopReason,
		},
		Usage: usage{
			OutputTokens: 0,
		},
	}
	writeSSE(w, "message_delta", msgDelta)

	msgStop := messageStopEvent{Type: "message_stop"}
	writeSSE(w, "message_stop", msgStop)

	flusher.Flush()
}

func handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logRequestBody(r)

	var req completionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	resp := completionResponse{
		Completion: catReply,
		StopReason: "stop_sequence",
		Stop:       "",
		LogID:      newID("log"),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "encode error", http.StatusInternalServerError)
		return
	}
}

func handleEventLogging(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	logRequestBody(r)
	w.WriteHeader(http.StatusNoContent)
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "event: %s\n", event)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

func newID(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}

func logRequestBody(r *http.Request) {
	if !logBodies {
		return
	}
	body, err := readBody(r)
	if err != nil {
		log.Printf("read body error: %v", err)
		return
	}
	if len(body) > logMaxBytes {
		body = body[:logMaxBytes]
	}
	log.Printf("body %s %s: %s", r.Method, r.URL.Path, string(body))
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

func streamTextBlock(w http.ResponseWriter, index int, text string) {
	blockStart := contentBlockStartEvent{
		Type:         "content_block_start",
		Index:        index,
		ContentBlock: contentBlock{Type: "text", Text: ""},
	}
	writeSSE(w, "content_block_start", blockStart)

	blockDelta := contentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: index,
		Delta: contentDelta{Type: "text_delta", Text: text},
	}
	writeSSE(w, "content_block_delta", blockDelta)

	blockStop := contentBlockStopEvent{
		Type:  "content_block_stop",
		Index: index,
	}
	writeSSE(w, "content_block_stop", blockStop)
}

func streamToolUseBlock(w http.ResponseWriter, index int, toolUse contentBlock) {
	blockStart := contentBlockStartEvent{
		Type:  "content_block_start",
		Index: index,
		ContentBlock: contentBlock{
			Type:  "tool_use",
			ID:    toolUse.ID,
			Name:  toolUse.Name,
			Input: map[string]any{},
		},
	}
	writeSSE(w, "content_block_start", blockStart)

	inputJSON, err := json.Marshal(toolUse.Input)
	if err != nil {
		inputJSON = []byte("{}")
	}
	blockDelta := contentBlockDeltaEvent{
		Type:  "content_block_delta",
		Index: index,
		Delta: contentDelta{
			Type:        "input_json_delta",
			PartialJSON: string(inputJSON),
		},
	}
	writeSSE(w, "content_block_delta", blockDelta)

	blockStop := contentBlockStopEvent{
		Type:  "content_block_stop",
		Index: index,
	}
	writeSSE(w, "content_block_stop", blockStop)
}

func mockToolUses() []contentBlock {
	if !enableToolCalls {
		return nil
	}
	return []contentBlock{
		{
			Type:  "tool_use",
			ID:    newID("toolu"),
			Name:  "shell",
			Input: map[string]any{"command": "echo '喵喵喵喵！' > 喵喵喵喵~"},
		},
		{
			Type:  "tool_use",
			ID:    newID("toolu"),
			Name:  "shell",
			Input: map[string]any{"command": "cat /tmp/meow.txt"},
		},
	}
}

func pickToolUses(req messageRequest) []contentBlock {
	if !enableToolCalls {
		return nil
	}
	if !hasTools(req.Tools) {
		return nil
	}
	if hasToolResult(req.Messages) {
		return nil
	}
	return mockToolUses()
}

func hasTools(tools any) bool {
	if tools == nil {
		return false
	}
	switch v := tools.(type) {
	case []any:
		return len(v) > 0
	default:
		return true
	}
}

func hasToolResult(messages any) bool {
	msgList, ok := messages.([]any)
	if !ok {
		return false
	}
	for _, msg := range msgList {
		msgMap, ok := msg.(map[string]any)
		if !ok {
			continue
		}
		content, ok := msgMap["content"]
		if !ok {
			continue
		}
		switch c := content.(type) {
		case []any:
			for _, item := range c {
				itemMap, ok := item.(map[string]any)
				if !ok {
					continue
				}
				itemType, ok := itemMap["type"].(string)
				if ok && itemType == "tool_result" {
					return true
				}
			}
		case map[string]any:
			itemType, ok := c["type"].(string)
			if ok && itemType == "tool_result" {
				return true
			}
		}
	}
	return false
}

func envBoolDefault(key string, def bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	switch strings.ToLower(val) {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return def
	}
}

func envIntDefault(key string, def int) int {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.Atoi(val)
	if err != nil {
		return def
	}
	return parsed
}
