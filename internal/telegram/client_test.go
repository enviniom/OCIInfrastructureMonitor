package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const testToken = "123456:test-secret-token"

func testCredentials() Credentials {
	return Credentials{BotToken: testToken, ChatID: "42"}
}

func TestClientSendsMessage(t *testing.T) {
	var got sendMessageRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if want := "/bot" + testToken + "/sendMessage"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode body: %v", err)
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
	}))
	defer server.Close()

	client, err := New(testCredentials(), Options{BaseURL: server.URL})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := client.SendMessage(context.Background(), "hola"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if got.ChatID != "42" || got.Text != "hola" {
		t.Errorf("request = %+v", got)
	}
	if !got.DisableWebPagePreview {
		t.Error("disable_web_page_preview = false")
	}
}

func TestClientAPIErrorIsPermanentAndRedacted(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`)
	}))
	defer server.Close()

	client, _ := New(testCredentials(), Options{BaseURL: server.URL})
	err := client.SendMessage(context.Background(), "hola")
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %#v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d", apiErr.StatusCode)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaked the token: %v", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (a 4xx is not retried)", requests)
	}
}

func TestClientRetriesServerErrors(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		current := requests
		mu.Unlock()
		if current < 3 {
			w.WriteHeader(http.StatusBadGateway)
			io.WriteString(w, `{"ok":false,"description":"bad gateway"}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	var waits []time.Duration
	client, _ := New(testCredentials(), Options{
		BaseURL:        server.URL,
		InitialBackoff: 10 * time.Millisecond,
		Sleep: func(_ context.Context, wait time.Duration) error {
			waits = append(waits, wait)
			return nil
		},
	})
	if err := client.SendMessage(context.Background(), "hola"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3", requests)
	}
	if len(waits) != 2 {
		t.Errorf("waits = %v, want 2 backoffs", waits)
	}
}

func TestClientHandles429WithRetryAfter(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		current := requests
		mu.Unlock()
		if current == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":1}}`)
			return
		}
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	var waits []time.Duration
	client, _ := New(testCredentials(), Options{
		BaseURL: server.URL,
		Sleep: func(_ context.Context, wait time.Duration) error {
			waits = append(waits, wait)
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := client.SendMessage(ctx, "hola"); err != nil {
		t.Fatalf("SendMessage() error = %v", err)
	}
	if len(waits) != 1 || waits[0] != time.Second {
		t.Errorf("waits = %v, want one 1s wait", waits)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2", requests)
	}
}

func TestClientLongRetryAfterDoesNotWait(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":60}}`)
	}))
	defer server.Close()

	slept := false
	client, _ := New(testCredentials(), Options{
		BaseURL: server.URL,
		Sleep: func(context.Context, time.Duration) error {
			slept = true
			return nil
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err := client.SendMessage(ctx, "hola")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	var retryErr *RetryAfterError
	if !errors.As(err, &retryErr) {
		t.Fatalf("error = %#v, want *RetryAfterError", err)
	}
	if retryErr.RetryAfter != 60*time.Second {
		t.Errorf("RetryAfter = %v, want 60s", retryErr.RetryAfter)
	}
	if slept {
		t.Error("the client waited for a retry_after that does not fit in the deadline")
	}
	if elapsed > time.Second {
		t.Errorf("elapsed = %v, want an immediate return", elapsed)
	}
	if strings.Contains(err.Error(), testToken) {
		t.Errorf("error leaked the token: %v", err)
	}
}

func TestClientTimeoutIsRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	client, _ := New(testCredentials(), Options{
		BaseURL:     server.URL,
		HTTPClient:  &http.Client{Timeout: 20 * time.Millisecond},
		MaxAttempts: 2,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	})
	err := client.SendMessage(context.Background(), "hola")
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaked the token: %v", err)
	}
}

func TestClientNetworkErrorIsRedacted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := server.URL
	server.Close()

	client, _ := New(testCredentials(), Options{
		BaseURL:     url,
		MaxAttempts: 2,
		Sleep:       func(context.Context, time.Duration) error { return nil },
	})
	err := client.SendMessage(context.Background(), "hola")
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("error leaked the token: %v", err)
	}
}

func TestClientRejectsInvalidMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the server must not be called")
	}))
	defer server.Close()
	client, _ := New(testCredentials(), Options{BaseURL: server.URL})

	if err := client.SendMessage(context.Background(), "   "); err == nil {
		t.Error("SendMessage(empty) error = nil")
	}
	long := strings.Repeat("á", MaxTextRunes+1)
	if err := client.SendMessage(context.Background(), long); err == nil {
		t.Error("SendMessage(long) error = nil")
	}
}

func TestNewRejectsInvalidCredentials(t *testing.T) {
	if _, err := New(Credentials{}, Options{}); err == nil {
		t.Fatal("New() error = nil")
	}
}

func TestRedactRemovesToken(t *testing.T) {
	client := &Client{token: testToken}
	got := client.redact("POST https://api.telegram.org/bot" + testToken + "/sendMessage: timeout")
	if strings.Contains(got, testToken) {
		t.Errorf("redact() = %q, token still present", got)
	}
	if !strings.Contains(got, "[REDACTADO]") {
		t.Errorf("redact() = %q, want a redaction marker", got)
	}
}

func TestClientRetriesTruncatedOKResponse(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":true,"result":`)
	}))
	defer server.Close()

	client, _ := New(testCredentials(), Options{
		BaseURL:        server.URL,
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		Sleep:          func(context.Context, time.Duration) error { return nil },
	})
	err := client.SendMessage(context.Background(), "hola")
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	var transportErr *TransportError
	if !errors.As(err, &transportErr) {
		t.Fatalf("error = %#v, want *TransportError", err)
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3 retries", requests)
	}
}

func TestClientOKFalseIsPermanent(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"ok":false,"description":"chat not found"}`)
	}))
	defer server.Close()

	client, _ := New(testCredentials(), Options{BaseURL: server.URL})
	err := client.SendMessage(context.Background(), "hola")
	if err == nil {
		t.Fatal("SendMessage() error = nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %#v, want *APIError", err)
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1", requests)
	}
}

func TestClient429HeaderRetryAfter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, `{"ok":false,"description":"Too Many Requests"}`)
	}))
	defer server.Close()

	client, _ := New(testCredentials(), Options{BaseURL: server.URL})
	result := client.sendOnce(context.Background(), "hola")
	if result.err == nil || !result.retry {
		t.Fatalf("result = %+v, want a retryable error", result)
	}
	var retryErr *RetryAfterError
	if !errors.As(result.err, &retryErr) {
		t.Fatalf("error = %#v, want *RetryAfterError", result.err)
	}
	if retryErr.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %s, want 7s", retryErr.RetryAfter)
	}
}
