package process

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/docvault/internal/store"
)

// LLM is a classifier that asks a chat model behind an OpenAI-compatible
// API: OpenRouter by default, or a local server (llama.cpp, Ollama) by
// setting the URL. The answer is held to the category and tag lists by a
// JSON schema, and checked again by Sanitize.
type LLM struct {
	URL   string // base URL, e.g. https://openrouter.ai/api/v1
	Key   string
	Model string
	HTTP  *http.Client
	// retryWait is the first wait after a 429 or 5xx (doubling each time).
	retryWait time.Duration
}

const OpenRouterURL = "https://openrouter.ai/api/v1"

func NewLLM(url, key, model string) *LLM {
	if url == "" {
		url = OpenRouterURL
	}
	return &LLM{URL: strings.TrimRight(url, "/"), Key: key, Model: model,
		HTTP: &http.Client{Timeout: 2 * time.Minute}, retryWait: 3 * time.Second}
}

func (l *LLM) Name() string { return "llm:" + l.Model }

func (l *LLM) openRouter() bool { return strings.Contains(l.URL, "openrouter.ai") }

const systemPrompt = `You file scanned household documents for an Indian family's document vault.
You get a document's current title and its text. The text comes from OCR, so expect misread words.
Personal details are replaced by placeholders: [aadhaar], [pan], [passport-no], [name], [address], [dob], [phone], [number] and so on mean that kind of value was there. [person:1], [person:2] and so on are members of the family.

Answer with JSON only:
- "category": the one category that fits, from the list. If none clearly fits, or you're unsure, return "" and the family sorts it by hand.
- "tags": at most 5 tags from the allowed list that clearly apply. When it's clear which family member the document belongs to or is about, add their placeholder (e.g. "[person:2]") as a tag. Only tags from the list go here.
- "new_tags": only when no tag on the list says what kind of document this is, up to 2 new tags in lowercase words joined by hyphens, like "airline-ticket". Reuse a tag from "other tags already in use" when one fits instead of making a variant of it. Usually empty.
- "title": a short, specific title a person would give it, like "Car insurance policy 2025-26", "Aadhaar card - [person:1]", "Blood test - Mar 2026", "Electricity bill - Sep 2026". Use a family member's placeholder for them; no other placeholders. If the current title is already good, return it.
- "doc_date": the date the document was issued or is about, as YYYY-MM-DD, or "" if unclear.
- "expires": the date it stops being valid (policy end, licence or passport validity, warranty end) as YYYY-MM-DD, or "" if it has none.`

// userPrompt lays out the choices and the document.
func userPrompt(in Input) string {
	var b strings.Builder
	b.WriteString("Categories, with the tags usually filed under each:\n")
	for _, c := range in.Categories {
		name := c.Name
		if name == "" {
			name = "(any category)"
		}
		if len(c.Tags) == 0 {
			fmt.Fprintf(&b, "- %s\n", name)
			continue
		}
		fmt.Fprintf(&b, "- %s: %s\n", name, strings.Join(c.Tags, ", "))
	}
	if len(in.People) > 0 {
		fmt.Fprintf(&b, "\nFamily members: %s\n", strings.Join(in.People, ", "))
	}
	if len(in.UsedTags) > 0 {
		fmt.Fprintf(&b, "\nOther tags already in use (for new_tags): %s\n", strings.Join(in.UsedTags, ", "))
	}
	if in.Text == "" {
		fmt.Fprintf(&b, "\nCurrent title: %s\n\nText: not sent. Decide from the title; leave doc_date and expires empty unless the title states them.\n", in.Title)
	} else {
		fmt.Fprintf(&b, "\nCurrent title: %s\n\nText:\n%s\n", in.Title, in.Text)
	}
	return b.String()
}

// schema holds the answer to the lists (for models that support it).
func schema(in Input) map[string]any {
	cats := []string{""}
	for _, c := range in.Categories {
		if c.Name != "" {
			cats = append(cats, c.Name)
		}
	}
	tags := map[string]any{"type": "string"}
	if len(in.Tags) > 0 {
		tags["enum"] = in.Tags
	}
	str := map[string]any{"type": "string"}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"title", "category", "tags", "new_tags", "doc_date", "expires"},
		"properties": map[string]any{
			"title":    str,
			"category": map[string]any{"type": "string", "enum": cats},
			"tags":     map[string]any{"type": "array", "items": tags},
			"new_tags": map[string]any{"type": "array", "items": str},
			"doc_date": str,
			"expires":  str,
		},
	}
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	MaxTokens      int            `json:"max_tokens"`
	ResponseFormat map[string]any `json:"response_format,omitempty"`
	// Provider is OpenRouter's routing: only providers that neither keep
	// nor train on prompts.
	Provider map[string]any `json:"provider,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (l *LLM) Classify(ctx context.Context, in Input) (*store.Suggest, error) {
	req := chatRequest{
		Model: l.Model, Temperature: 0, MaxTokens: 400,
		Messages: []chatMessage{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt(in)}},
		ResponseFormat: map[string]any{"type": "json_schema", "json_schema": map[string]any{
			"name": "filing", "strict": true, "schema": schema(in),
		}},
	}
	if l.openRouter() {
		req.Provider = map[string]any{"data_collection": "deny", "zdr": true}
	}
	body, _ := json.Marshal(req)

	var resp *chatResponse
	var err error
	wait := l.retryWait
	for attempt := 0; ; attempt++ {
		var retryAfter time.Duration
		resp, retryAfter, err = l.post(ctx, body)
		if err == nil || retryAfter < 0 || attempt == 3 {
			break
		}
		if retryAfter > 0 {
			wait = retryAfter
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
	}
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("the model gave no answer")
	}
	slog.Debug("classified", "document", in.DocumentID, "model", l.Model,
		"in", resp.Usage.PromptTokens, "out", resp.Usage.CompletionTokens)
	var sg store.Suggest
	if err := json.Unmarshal([]byte(jsonPart(resp.Choices[0].Message.Content)), &sg); err != nil {
		return nil, fmt.Errorf("the model's answer isn't JSON: %w", err)
	}
	return &sg, nil
}

// post sends one request. retryAfter is how long to wait before trying
// again (0: the default wait, -1: don't).
func (l *LLM) post(ctx context.Context, body []byte) (*chatResponse, time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.URL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, -1, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+l.Key)
	if l.openRouter() {
		req.Header.Set("X-Title", "Docvault")
	}
	res, err := l.HTTP.Do(req)
	if err != nil {
		return nil, 0, err // network trouble: try again
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return nil, 0, err
	}
	var out chatResponse
	jsonErr := json.Unmarshal(raw, &out)
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		after := time.Duration(0)
		if s, err := strconv.Atoi(res.Header.Get("Retry-After")); err == nil && s > 0 {
			after = min(time.Duration(s)*time.Second, 2*time.Minute)
		}
		return nil, after, fmt.Errorf("the model's API answered %s%s", res.Status, apiMessage(&out))
	}
	if res.StatusCode != http.StatusOK {
		return nil, -1, fmt.Errorf("the model's API answered %s%s", res.Status, apiMessage(&out))
	}
	if jsonErr != nil {
		return nil, -1, fmt.Errorf("the model's API answer isn't JSON: %w", jsonErr)
	}
	if out.Error != nil { // some APIs answer 200 with an error
		return nil, -1, fmt.Errorf("the model's API: %s", out.Error.Message)
	}
	return &out, 0, nil
}

func apiMessage(r *chatResponse) string {
	if r.Error != nil && r.Error.Message != "" {
		return ": " + r.Error.Message
	}
	return ""
}

// jsonPart takes the JSON object out of an answer that may wrap it in a
// code fence or a sentence.
func jsonPart(s string) string {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
