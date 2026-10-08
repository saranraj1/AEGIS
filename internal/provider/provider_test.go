package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient builds an OpenAI client pointed at srv (with a /v1 base
// path and trailing slash, exercising URL trimming) and instant backoff
// so retry tests run fast.
func newTestClient(t *testing.T, srv *httptest.Server) *OpenAI {
	t.Helper()
	c := NewOpenAI(srv.URL+"/v1/", "sk-test-key", "test-model")
	c.Backoff = func(int) time.Duration { return 0 }
	return c
}

func chatOK(content string) string {
	return fmt.Sprintf(`{"choices":[{"message":{"role":"assistant","content":%s}}]}`, mustJSON(content))
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestFromEnvMissingVars(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-sentinel-value")
	t.Setenv(EnvBaseURL, "https://api.example.com/v1")
	t.Setenv(EnvModel, "")

	_, err := FromEnv()
	if err == nil {
		t.Fatal("expected error for missing env vars")
	}
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindMissingEnv {
		t.Fatalf("got %v, want kind %s", err, KindMissingEnv)
	}
	if !strings.Contains(perr.Msg, EnvModel) || strings.Contains(perr.Msg, EnvAPIKey) {
		t.Errorf("error should name only the missing var: %q", perr.Msg)
	}
	// The key's value must never appear in the error.
	if strings.Contains(perr.Msg, "sk-sentinel-value") {
		t.Errorf("error leaked key value: %q", perr.Msg)
	}
}

func TestFromEnvAllMissing(t *testing.T) {
	t.Setenv(EnvAPIKey, "")
	t.Setenv(EnvBaseURL, "")
	t.Setenv(EnvModel, "")

	_, err := FromEnv()
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindMissingEnv {
		t.Fatalf("got %v, want kind %s", err, KindMissingEnv)
	}
	for _, name := range []string{EnvAPIKey, EnvBaseURL, EnvModel} {
		if !strings.Contains(perr.Msg, name) {
			t.Errorf("error should name %s: %q", name, perr.Msg)
		}
	}
}

func TestFromEnvAllSet(t *testing.T) {
	t.Setenv(EnvAPIKey, "sk-x")
	t.Setenv(EnvBaseURL, "https://api.example.com/v1/")
	t.Setenv(EnvModel, "m1")

	c, err := FromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.APIKey != "sk-x" || c.Model != "m1" {
		t.Errorf("client fields not populated: %+v", c)
	}
	if strings.HasSuffix(c.BaseURL, "/") {
		t.Errorf("trailing slash not trimmed: %q", c.BaseURL)
	}
}

func TestOpenAICallHappyPath(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody chatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, chatOK(`{"hello":"world"}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.Call(context.Background(), Request{
		Stage:  StageClaims,
		System: "system prompt",
		User:   "user prompt",
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test-key" {
		t.Errorf("authorization = %q", gotAuth)
	}
	if gotBody.Model != "test-model" {
		t.Errorf("model = %q", gotBody.Model)
	}
	if gotBody.Temperature != 0 {
		t.Errorf("temperature = %v, want 0", gotBody.Temperature)
	}
	if gotBody.ResponseFormat == nil || gotBody.ResponseFormat.Type != "json_object" {
		t.Errorf("response_format missing or wrong: %+v", gotBody.ResponseFormat)
	}
	if len(gotBody.Messages) != 2 ||
		gotBody.Messages[0].Role != "system" || gotBody.Messages[0].Content != "system prompt" ||
		gotBody.Messages[1].Role != "user" || gotBody.Messages[1].Content != "user prompt" {
		t.Errorf("messages = %+v", gotBody.Messages)
	}
	if resp.Content != `{"hello":"world"}` {
		t.Errorf("content = %q", resp.Content)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", resp.Warnings)
	}
}

func TestOpenAIRetry429ThenSuccess(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 2 {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, chatOK("ok"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.Call(context.Background(), Request{Stage: StageWrap})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("content = %q", resp.Content)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want 3", got)
	}
}

func TestOpenAIRetryExhausted(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Call(context.Background(), Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindProviderTransient {
		t.Fatalf("got %v, want kind %s", err, KindProviderTransient)
	}
	if !strings.Contains(perr.Msg, "503") {
		t.Errorf("error should mention last status: %q", perr.Msg)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("requests = %d, want exactly 3 attempts", got)
	}
}

func TestOpenAI4xxHardError(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"message":"unknown model"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Call(context.Background(), Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindProvider4xx {
		t.Fatalf("got %v, want kind %s", err, KindProvider4xx)
	}
	if !strings.Contains(perr.Msg, "unknown model") {
		t.Errorf("error should include server detail: %q", perr.Msg)
	}
	if got := calls.Load(); got != 2 {
		// One 4xx triggers the response_format fallback (C6), so exactly
		// two requests: proves 4xx never enters the transient retry loop.
		t.Errorf("requests = %d, want 2 (no transient retry on 4xx)", got)
	}
}

func TestOpenAIResponseFormatFallback(t *testing.T) {
	var calls atomic.Int64
	var secondHadRF bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body chatRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		if calls.Add(1) == 1 {
			if body.ResponseFormat == nil {
				t.Error("first request should carry response_format")
			}
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"response_format unsupported"}}`)
			return
		}
		secondHadRF = body.ResponseFormat != nil
		fmt.Fprint(w, chatOK(`{"fine":true}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	resp, err := c.Call(context.Background(), Request{Stage: StageClaims})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if secondHadRF {
		t.Error("retry must omit response_format")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2", got)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "response_format") {
		t.Errorf("warnings = %v, want one response_format warning", resp.Warnings)
	}
	if resp.Content != `{"fine":true}` {
		t.Errorf("content = %q", resp.Content)
	}
}

func TestOpenAIResponseFormatFallbackStill4xx(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	_, err := c.Call(context.Background(), Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindProvider4xx {
		t.Fatalf("got %v, want kind %s", err, KindProvider4xx)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("requests = %d, want 2 (one per mode)", got)
	}
}

func TestOpenAIKeyNeverLeakedInErrors(t *testing.T) {
	secret := "sk-very-secret-value"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, `{"error":{"message":"rejected key %s"}}`, secret)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	c.APIKey = secret
	_, err := c.Call(context.Background(), Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindProvider4xx {
		t.Fatalf("got %v, want kind %s", err, KindProvider4xx)
	}
	if strings.Contains(perr.Msg, secret) {
		t.Errorf("error leaked the API key: %q", perr.Msg)
	}
}

func TestOpenAITimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, chatOK("late"))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Call(ctx, Request{Stage: StageWrap})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindTimeout {
		t.Fatalf("got %v, want kind %s", err, KindTimeout)
	}
}

func TestOpenAIBadResponse(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"no choices", `{"choices":[]}`},
		{"invalid json", `not json at all`},
		{"wrong shape", `{"result":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()

			c := newTestClient(t, srv)
			_, err := c.Call(context.Background(), Request{Stage: StageWrap})
			var perr *Error
			if !errors.As(err, &perr) || perr.Kind != KindBadResponse {
				t.Fatalf("got %v, want kind %s", err, KindBadResponse)
			}
		})
	}
}

func TestOpenAIInvalidStage(t *testing.T) {
	c := NewOpenAI("http://unused", "k", "m")
	_, err := c.Call(context.Background(), Request{Stage: "bogus"})
	var perr *Error
	if !errors.As(err, &perr) || perr.Kind != KindBadRequest {
		t.Fatalf("got %v, want kind %s", err, KindBadRequest)
	}
}
