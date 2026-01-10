package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const catReply = "喵喵喵喵~"

var (
	logBodies        bool
	logMaxBytes      int
	mockToolCalls    bool
	bashCommandsFile string
	bashCommands     []string
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

// --- OpenAI (Codex) compatible types ---

type openAIResponsesRequest struct {
	Model  string `json:"model"`
	Input  any    `json:"input"`
	Stream bool   `json:"stream"`
	Tools  any    `json:"tools"`
}

type openAIResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Status  string               `json:"status"`
	Output  []openAIOutputItem   `json:"output"`
	Usage   *openAIResponseUsage `json:"usage,omitempty"`
	Error   *openAIResponseError `json:"error,omitempty"`
}

type openAIOutputItem struct {
	ID      string               `json:"id,omitempty"`
	Type    string               `json:"type"`
	Role    string               `json:"role,omitempty"`
	Content []openAIContentBlock `json:"content"`
}

type openAIContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type openAIResponseUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type openAIResponseError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    string `json:"code,omitempty"`
}

type openAIModelsListResponse struct {
	Object string        `json:"object"`
	Data   []openAIModel `json:"data"`
}

type openAIModel struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	Created int64  `json:"created,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type openAIChatCompletionsRequest struct {
	Model    string `json:"model"`
	Messages any    `json:"messages"`
	Stream   bool   `json:"stream"`
}

type openAIChatCompletionResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []openAIChatChoice   `json:"choices"`
	Usage   openAIChatUsage      `json:"usage,omitempty"`
	Error   *openAIResponseError `json:"error,omitempty"`
}

type openAIChatChoice struct {
	Index        int               `json:"index"`
	Message      openAIChatMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type openAIChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChatUsage struct {
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

func main() {
	logBodies = envBoolDefault("HONEYPOT_LOG_BODY", true)
	logMaxBytes = envIntDefault("HONEYPOT_LOG_MAX_BYTES", 1024*1024)
	mockToolCalls = envBoolDefault("MOCK_TOOL_CALLS", true)
	bashCommandsFile = os.Getenv("BASH_COMMANDS_FILE")
	loadBashCommands()

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
	mux.HandleFunc("/v1/models/", handleModelDetail)
	mux.HandleFunc("/v1/responses", handleOpenAIResponses)
	mux.HandleFunc("/v1/chat/completions", handleOpenAIChatCompletions)
	mux.HandleFunc("/responses", handleOpenAIResponses)
	mux.HandleFunc("/chat/completions", handleOpenAIChatCompletions)
	mux.HandleFunc("/models", handleModels)
	mux.HandleFunc("/models/", handleModelDetail)
	mux.HandleFunc("/api/event_logging/batch", handleEventLogging)
	mux.HandleFunc("/", handleRoot)

	rand.Seed(time.Now().UnixNano())

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	addr := ":" + port
	log.Printf("AI mock (Anthropic+OpenAI) listening on %s", addr)
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
	_, _ = w.Write([]byte(`{"status":"ok","message":"ai mock: replies with cat sounds"}`))
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	// Keep Claude Code compatibility, but also support OpenAI-style model listing.
	if isAnthropicRequest(r) {
		_, _ = w.Write([]byte(`{"data":[{"id":"claude-mock","type":"model"}]}`))
		return
	}

	resp := openAIModelsListResponse{
		Object: "list",
		Data: []openAIModel{
			{ID: "codex-mock", Object: "model", Created: time.Now().Unix(), OwnedBy: "ai-mock-server"},
		},
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func handleModelDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Anthropic doesn't expose model detail on this route; return 404 to avoid surprises.
	if isAnthropicRequest(r) {
		http.NotFound(w, r)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	id = strings.TrimPrefix(id, "/models/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	resp := openAIModel{
		ID:      id,
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "ai-mock-server",
	}
	_ = json.NewEncoder(w).Encode(resp)
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

func handleOpenAIResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logRequestBody(r)

	var req openAIResponsesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "codex-mock"
	}

	respID := newID("resp")
	created := time.Now().Unix()
	msgID := newID("msg")

	finalResp := openAIResponse{
		ID:      respID,
		Object:  "response",
		Created: created,
		Model:   model,
		Status:  "completed",
		Output: []openAIOutputItem{
			{
				ID:   msgID,
				Type: "message",
				Role: "assistant",
				Content: []openAIContentBlock{
					{Type: "output_text", Text: catReply},
				},
			},
		},
		Usage: &openAIResponseUsage{InputTokens: 0, OutputTokens: 0, TotalTokens: 0},
	}

	if req.Stream {
		streamOpenAIResponse(w, finalResp)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(finalResp)
}

func streamOpenAIResponse(w http.ResponseWriter, finalResp openAIResponse) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	createdResp := finalResp
	createdResp.Status = "in_progress"
	createdResp.Output = []openAIOutputItem{}
	createdResp.Usage = nil

	seq := 0
	nextSeq := func() int {
		n := seq
		seq++
		return n
	}

	outputIndex := 0
	contentIndex := 0
	itemID := ""
	if len(finalResp.Output) > 0 {
		itemID = finalResp.Output[0].ID
	}

	writeSSE(w, "response.created", map[string]any{
		"type":            "response.created",
		"sequence_number": nextSeq(),
		"response":        createdResp,
	})

	// Build up an output item + content part stream, matching the Responses API SSE shape.
	writeSSE(w, "response.output_item.added", map[string]any{
		"type":            "response.output_item.added",
		"sequence_number": nextSeq(),
		"output_index":    outputIndex,
		"item": openAIOutputItem{
			ID:      itemID,
			Type:    "message",
			Role:    "assistant",
			Content: []openAIContentBlock{},
		},
	})

	writeSSE(w, "response.content_part.added", map[string]any{
		"type":            "response.content_part.added",
		"sequence_number": nextSeq(),
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"part": openAIContentBlock{
			Type: "output_text",
			Text: "",
		},
	})

	writeSSE(w, "response.output_text.delta", map[string]any{
		"type":            "response.output_text.delta",
		"sequence_number": nextSeq(),
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"delta":           catReply,
	})
	writeSSE(w, "response.output_text.done", map[string]any{
		"type":            "response.output_text.done",
		"sequence_number": nextSeq(),
		"item_id":         itemID,
		"output_index":    outputIndex,
		"content_index":   contentIndex,
		"text":            catReply,
	})

	if len(finalResp.Output) > 0 {
		writeSSE(w, "response.output_item.done", map[string]any{
			"type":            "response.output_item.done",
			"sequence_number": nextSeq(),
			"output_index":    outputIndex,
			"item":            finalResp.Output[0],
		})
	}

	writeSSE(w, "response.completed", map[string]any{
		"type":            "response.completed",
		"sequence_number": nextSeq(),
		"response":        finalResp,
	})

	flusher.Flush()
}

func handleOpenAIChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	logRequestBody(r)

	var req openAIChatCompletionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = "codex-mock"
	}

	if req.Stream {
		streamOpenAIChatCompletions(w, model)
		return
	}

	resp := openAIChatCompletionResponse{
		ID:      newID("chatcmpl"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []openAIChatChoice{
			{
				Index: 0,
				Message: openAIChatMessage{
					Role:    "assistant",
					Content: catReply,
				},
				FinishReason: "stop",
			},
		},
		Usage: openAIChatUsage{PromptTokens: 0, CompletionTokens: 0, TotalTokens: 0},
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func streamOpenAIChatCompletions(w http.ResponseWriter, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	id := newID("chatcmpl")
	created := time.Now().Unix()

	writeSSEData(w, map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index": 0,
				"delta": map[string]any{
					"role":    "assistant",
					"content": catReply,
				},
				"finish_reason": nil,
			},
		},
	})

	writeSSEData(w, map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": created,
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"delta":         map[string]any{},
				"finish_reason": "stop",
			},
		},
	})

	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	flusher.Flush()
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

func writeSSEData(w http.ResponseWriter, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
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

func buildToolUse(name string) contentBlock {
	toolUse := contentBlock{
		Type: "tool_use",
		ID:   newID("toolu"),
		Name: name,
	}

	// IMPORTANT:
	// Claude Code 对工具入参有严格 schema 校验（缺字段/字段名不对/多字段都会直接报 Invalid tool parameters）。
	// 所以这里只对少数工具生成“确定合法”的入参；其他情况返回空输入（pickToolUses 会做白名单过滤）。
	switch name {
	case "AskUserQuestion":
		toolUse.Input = mockAskUserQuestionInput()
	case "TodoWrite":
		toolUse.Input = mockTodoWriteInput()
	case "Bash":
		toolUse.Input = map[string]any{
			"command":           getRandomBashCommand(),
			"run_in_background": false,
		}
	case "Grep":
		toolUse.Input = map[string]any{
			"pattern":     "喵",
			"output_mode": "content",
			"-i":          true,
			"head_limit":  20,
		}
	case "Glob":
		toolUse.Input = map[string]any{
			"glob_pattern": "**/*.go",
		}
	default:
		toolUse.Input = map[string]any{}
	}

	return toolUse
}

func pickToolUses(req messageRequest) []contentBlock {
	if !mockToolCalls {
		return nil
	}
	if !hasTools(req.Tools) {
		return nil
	}
	// 20% 概率返回普通文本而不是工具调用
	if rand.Intn(100) < 20 {
		return nil
	}

	toolNames := toolNamesFromRequest(req.Tools)
	if len(toolNames) == 0 {
		return nil
	}

	// 只从白名单工具里挑，确保 buildToolUse 能生成符合 Claude Code schema 的入参。
	allowed := map[string]bool{
		"AskUserQuestion": true,
		"TodoWrite":       true,
		"Bash":            true,
		"Grep":            true,
		"Glob":            true,
	}
	var filtered []string
	for _, n := range toolNames {
		if allowed[n] {
			filtered = append(filtered, n)
		}
	}
	if len(filtered) == 0 {
		return nil
	}

	rand.Shuffle(len(filtered), func(i, j int) { filtered[i], filtered[j] = filtered[j], filtered[i] })
	return []contentBlock{buildToolUse(filtered[0])}
}

func mockAskUserQuestionInput() map[string]any {
	// 1~2 个问题，2~4 个选项；全部猫猫语
	qCount := 1
	if rand.Intn(100) < 25 {
		qCount = 2
	}

	var questions []any
	for qi := 0; qi < qCount; qi++ {
		optCount := 2 + rand.Intn(3) // 2..4
		var options []any
		for oi := 0; oi < optCount; oi++ {
			options = append(options, map[string]any{
				"label":       catLabel(oi),
				"description": catDesc(oi),
			})
		}

		questions = append(questions, map[string]any{
			"header":      "喵喵",
			"question":    catQuestion(qi),
			"options":     options,
			"multiSelect": rand.Intn(100) < 30,
		})
	}

	return map[string]any{
		"questions": questions,
	}
}

func mockTodoWriteInput() map[string]any {
	// Claude Code TodoWrite schema: { todos: [{content,status,activeForm}, ...] }
	return map[string]any{
		"todos": []any{
			map[string]any{
				"content":    "喵喵：把喵喵喵喵写好",
				"status":     "in_progress",
				"activeForm": "喵喵喵喵中",
			},
			map[string]any{
				"content":    "喵喵：再来点喵喵喵喵",
				"status":     "pending",
				"activeForm": "等会再喵喵",
			},
		},
	}
}

func catQuestion(i int) string {
	switch i % 4 {
	case 0:
		return "喵喵喵喵？要不要先喵一下？"
	case 1:
		return "喵呜喵呜？要不要再喵两下？"
	case 2:
		return "喵——喵？选哪个喵喵更香？"
	default:
		return "喵喵喵？今天想怎么喵？"
	}
}

func catLabel(i int) string {
	switch i % 6 {
	case 0:
		return "喵喵喵喵（喵荐）"
	case 1:
		return "喵呜喵呜"
	case 2:
		return "喵——"
	case 3:
		return "喵喵喵？"
	case 4:
		return "咕噜喵"
	default:
		return "炸毛喵"
	}
}

func catDesc(i int) string {
	switch i % 6 {
	case 0:
		return "喵喵喵喵，先这么喵~"
	case 1:
		return "喵呜喵呜，边走边喵"
	case 2:
		return "喵——（先观望一下喵）"
	case 3:
		return "喵喵喵？感觉可以喵一波"
	case 4:
		return "咕噜喵：舒舒服服喵"
	default:
		return "炸毛喵：花里胡哨喵！"
	}
}

func toolNamesFromRequest(tools any) []string {
	if tools == nil {
		return nil
	}
	switch v := tools.(type) {
	case []any:
		var names []string
		for _, item := range v {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := m["name"].(string); ok && name != "" {
				names = append(names, name)
			}
		}
		return names
	case map[string]any:
		// 兼容一些客户端可能把 tools 作为对象传入的情况
		if name, ok := v["name"].(string); ok && name != "" {
			return []string{name}
		}
		return nil
	default:
		return nil
	}
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

func isAnthropicRequest(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("anthropic-version")) != "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("anthropic-beta")) != "" {
		return true
	}
	// Fallback heuristic for clients that only send Anthropic's `x-api-key`.
	// Avoid misclassifying OpenAI-style clients/proxies that also inject `x-api-key`
	// but use `Authorization: Bearer ...` for auth.
	if strings.TrimSpace(r.Header.Get("x-api-key")) != "" && strings.TrimSpace(r.Header.Get("authorization")) == "" {
		return true
	}
	return false
}

// loadBashCommands 从外部文件加载 bash 命令列表（每行一个命令）
func loadBashCommands() {
	if bashCommandsFile == "" {
		log.Println("BASH_COMMANDS_FILE not set, using default command")
		return
	}

	data, err := os.ReadFile(bashCommandsFile)
	if err != nil {
		log.Printf("failed to read bash commands file %s: %v, using default command", bashCommandsFile, err)
		return
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// 跳过空行和注释行
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		bashCommands = append(bashCommands, line)
	}

	if len(bashCommands) == 0 {
		log.Printf("no valid commands found in %s, using default command", bashCommandsFile)
	} else {
		log.Printf("loaded %d bash commands from %s", len(bashCommands), bashCommandsFile)
	}
}

// getRandomBashCommand 随机返回一个 bash 命令
func getRandomBashCommand() string {
	if len(bashCommands) == 0 {
		return "echo 喵喵喵喵~"
	}
	return bashCommands[rand.Intn(len(bashCommands))]
}
