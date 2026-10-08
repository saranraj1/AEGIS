package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAI is the live provider: an OpenAI-compatible chat-completions
// client using only net/http and encoding/json (no SDK, per A6).
type OpenAI struct {
	// BaseURL is the endpoint root, e.g. "https://api.example.com/v1".
	// A trailing slash is trimmed; the client posts to
	// {BaseURL}/chat/completions.
	BaseURL string
	// APIKey is sent as a Bearer token. It is never written to error
	// messages, logs, or reports.
	APIKey string
	// Model is sent in every request body.
	Model string
	// HTTPClient is optional; nil means http.DefaultClient.
	HTTPClient *http.Client
	// MaxAttempts is the retry budget for 429/5xx responses per
	// request mode. Zero or negative means the spec default of 3.
	MaxAttempts int
	// Backoff returns the sleep duration before the next attempt.
	// attempt is 1-based and the function is only called when more
	// attempts remain. Zero means the spec default (exponential from
	// 250ms). Tests inject a zero-duration backoff.
	Backoff func(attempt int) time.Duration
}

// NewOpenAI returns a client with spec defaults for a resolved
// base URL, API key, and model.
func NewOpenAI(baseURL, apiKey, model string) *OpenAI {
	return &OpenAI{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
	}
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Call posts one stage request. The caller bounds the call with the ctx
// deadline (per-stage timeout); ctx expiry surfaces as a KindTimeout
// error, never a retry.
func (c *OpenAI) Call(ctx context.Context, req Request) (Response, error) {
	if !req.Stage.Valid() {
		return Response{}, &Error{Kind: KindBadRequest, Msg: fmt.Sprintf("invalid stage %q", req.Stage)}
	}
	maxAttempts := c.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}

	var warnings []string
	for mode := 0; mode < 2; mode++ {
		useRF := mode == 0
		switchMode := false
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			content, err := c.attemptOnce(ctx, req, useRF)
			if err == nil {
				return Response{Content: content, Warnings: warnings}, nil
			}
			perr := err.(*Error)
			switch perr.Kind {
			case KindProviderTransient:
				if attempt < maxAttempts {
					c.sleep(attempt)
					continue
				}
				// Last attempt failed; report exhaustion.
				return Response{}, perr
			case KindProvider4xx:
				if useRF {
					// C6: the server rejected the request while
					// response_format was set. Retry without the
					// field and record a warning.
					warnings = append(warnings, "response_format not supported by endpoint; retried without it")
					switchMode = true
				} else {
					return Response{}, perr
				}
			default:
				return Response{}, perr
			}
			if switchMode {
				break
			}
		}
	}
	// Unreachable: both modes return before falling out of the loops.
	return Response{}, &Error{Kind: KindBadRequest, Msg: "exhausted request modes"}
}

func (c *OpenAI) sleep(attempt int) {
	if c.Backoff == nil {
		time.Sleep(backoffDuration(attempt))
		return
	}
	time.Sleep(c.Backoff(attempt))
}

func backoffDuration(attempt int) time.Duration {
	d := 250 * time.Millisecond << (attempt - 1)
	if d > 4*time.Second {
		return 4 * time.Second
	}
	return d
}

// attemptOnce performs exactly one HTTP round trip and classifies the
// outcome into the error kinds above.
func (c *OpenAI) attemptOnce(ctx context.Context, req Request, useResponseFormat bool) (string, error) {
	body := chatRequest{
		Model:       c.Model,
		Messages:    []chatMessage{{Role: "system", Content: req.System}, {Role: "user", Content: req.User}},
		Temperature: 0,
	}
	if useResponseFormat {
		body.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", &Error{Kind: KindBadRequest, Msg: "marshal request: " + err.Error()}
	}

	url := c.BaseURL + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", &Error{Kind: KindBadRequest, Msg: "build request: " + err.Error()}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", &Error{Kind: KindTimeout, Msg: "stage call timed out: " + ctxErr.Error()}
		}
		return "", &Error{Kind: KindBadResponse, Msg: "transport error: " + err.Error()}
	}
	defer httpResp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(httpResp.Body, 1<<20))
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", &Error{Kind: KindTimeout, Msg: "stage call timed out: " + ctxErr.Error()}
		}
		return "", &Error{Kind: KindBadResponse, Msg: "read response body: " + err.Error()}
	}

	status := httpResp.StatusCode
	if status >= 400 {
		detail := serverMessage(raw)
		detail = scrub(c.APIKey, detail)
		if status == http.StatusTooManyRequests || status >= 500 {
			return "", &Error{
				Kind: KindProviderTransient,
				Msg:  fmt.Sprintf("transient provider failure (status %d): %s", status, detail),
			}
		}
		return "", &Error{
			Kind: KindProvider4xx,
			Msg:  fmt.Sprintf("provider rejected request (status %d): %s", status, detail),
		}
	}

	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", &Error{Kind: KindBadResponse, Msg: "response is not a valid chat completion: " + err.Error()}
	}
	if len(parsed.Choices) == 0 {
		return "", &Error{Kind: KindBadResponse, Msg: "chat completion contains no choices"}
	}
	return parsed.Choices[0].Message.Content, nil
}

// serverMessage extracts a bounded human-readable detail from an error
// body, preferring the OpenAI-style {"error":{"message":...}} shape.
func serverMessage(raw []byte) string {
	var wrapper struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrapper); err == nil && wrapper.Error != nil && wrapper.Error.Message != "" {
		return wrapper.Error.Message
	}
	const max = 300
	s := string(raw)
	if len(s) > max {
		s = s[:max] + "..."
	}
	return strings.TrimSpace(s)
}

// scrub removes any occurrence of the API key from an error detail so a
// misbehaving endpoint cannot cause the key to leak into reports or logs.
func scrub(secret, s string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}
