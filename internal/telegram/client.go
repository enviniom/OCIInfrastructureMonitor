package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Defaults of the Telegram transport. The global deadline of a run is larger
// than these values; see app.RunOptions.Deadline.
const (
	DefaultBaseURL        = "https://api.telegram.org"
	DefaultTimeout        = 10 * time.Second
	DefaultMaxAttempts    = 3
	DefaultInitialBackoff = 500 * time.Millisecond
	DefaultMaxBackoff     = 5 * time.Second
	DefaultMinSendBudget  = 2 * time.Second
	// MaxTextRunes is the Telegram message limit.
	MaxTextRunes = 4096
)

// Sender delivers one text message. The run command depends on this interface,
// not on the concrete client, so tests can use an in-memory sender.
type Sender interface {
	SendMessage(ctx context.Context, text string) error
}

// APIError is a non-retryable (or retried) HTTP error returned by Telegram.
type APIError struct {
	StatusCode  int
	Description string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("telegram API error: status %d: %s", e.StatusCode, e.Description)
}

// RetryAfterError means Telegram answered 429 with a wait that does not fit in
// the remaining deadline. The caller must keep the notification queued and exit
// with the Telegram code instead of waiting for systemd to kill the process.
type RetryAfterError struct {
	RetryAfter  time.Duration
	Description string
}

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("telegram rate limit: retry after %s: %s", e.RetryAfter.Round(time.Second), e.Description)
}

// TransportError is a network, timeout or context error with the token removed
// from its text. It deliberately does not unwrap the original error, because
// that error (a *url.Error) contains the request URL with the bot token.
type TransportError struct {
	message string
}

func (e *TransportError) Error() string { return e.message }

// Options configures the HTTP client. Zero values select the defaults.
type Options struct {
	BaseURL        string
	HTTPClient     *http.Client
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MinSendBudget  time.Duration
	// Sleep waits between attempts. Tests inject a recorder; production uses a
	// context-aware timer.
	Sleep func(context.Context, time.Duration) error
}

func (o Options) withDefaults() Options {
	if o.BaseURL == "" {
		o.BaseURL = DefaultBaseURL
	}
	if o.HTTPClient == nil {
		o.HTTPClient = &http.Client{Timeout: DefaultTimeout}
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.InitialBackoff <= 0 {
		o.InitialBackoff = DefaultInitialBackoff
	}
	if o.MaxBackoff <= 0 {
		o.MaxBackoff = DefaultMaxBackoff
	}
	if o.MinSendBudget <= 0 {
		o.MinSendBudget = DefaultMinSendBudget
	}
	if o.Sleep == nil {
		o.Sleep = sleepContext
	}
	return o
}

// Client sends messages to the Telegram Bot API with net/http only.
type Client struct {
	endpoint       string
	token          string
	chatID         string
	httpClient     *http.Client
	maxAttempts    int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	minSendBudget  time.Duration
	sleep          func(context.Context, time.Duration) error
}

// New validates the credentials and builds a client. The endpoint (which
// contains the token) is never exposed by an exported method.
func New(credentials Credentials, options Options) (*Client, error) {
	if err := credentials.Validate(); err != nil {
		return nil, err
	}
	options = options.withDefaults()
	base := strings.TrimRight(options.BaseURL, "/")
	if base == "" {
		return nil, errors.New("telegram API base URL is empty")
	}
	return &Client{
		endpoint:       base + "/bot" + credentials.BotToken + "/sendMessage",
		token:          credentials.BotToken,
		chatID:         credentials.ChatID,
		httpClient:     options.HTTPClient,
		maxAttempts:    options.MaxAttempts,
		initialBackoff: options.InitialBackoff,
		maxBackoff:     options.MaxBackoff,
		minSendBudget:  options.MinSendBudget,
		sleep:          options.Sleep,
	}, nil
}

type sendMessageRequest struct {
	ChatID                string `json:"chat_id"`
	Text                  string `json:"text"`
	DisableWebPagePreview bool   `json:"disable_web_page_preview"`
}

type apiResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

type attemptResult struct {
	err         error
	retry       bool
	retryAfter  time.Duration
	description string
}

// SendMessage delivers text, retrying network errors, 5xx responses and 429
// rate limits with backoff. A 429 whose retry_after does not fit in the context
// deadline returns *RetryAfterError immediately. Every returned error has the
// bot token redacted.
func (c *Client) SendMessage(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return &TransportError{message: "telegram message is empty"}
	}
	if utf8.RuneCountInString(text) > MaxTextRunes {
		return &TransportError{message: fmt.Sprintf("telegram message has %d characters, limit is %d", utf8.RuneCountInString(text), MaxTextRunes)}
	}

	var lastErr error
	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return &TransportError{message: "telegram request canceled: " + err.Error()}
		}

		result := c.sendOnce(ctx, text)
		if result.err == nil {
			return nil
		}
		lastErr = result.err
		if !result.retry {
			return result.err
		}

		wait := result.retryAfter
		if wait <= 0 {
			wait = c.backoff(attempt)
		}
		if !c.fits(ctx, wait) {
			// The wait would cross the run deadline: keep the notification
			// queued and let the caller exit with the Telegram code.
			return result.err
		}
		if attempt == c.maxAttempts {
			break
		}
		if err := c.sleep(ctx, wait); err != nil {
			return &TransportError{message: "telegram request canceled: " + err.Error()}
		}
	}
	return lastErr
}

func (c *Client) sendOnce(ctx context.Context, text string) attemptResult {
	payload, err := json.Marshal(sendMessageRequest{
		ChatID:                c.chatID,
		Text:                  text,
		DisableWebPagePreview: true,
	})
	if err != nil {
		return attemptResult{err: &TransportError{message: "encode telegram message: " + c.redact(err.Error())}}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return attemptResult{err: &TransportError{message: "build telegram request: " + c.redact(err.Error())}}
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return attemptResult{err: c.transportError(ctx, err), retry: true}
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var decoded apiResponse
	decodeErr := json.Unmarshal(body, &decoded)
	description := strings.TrimSpace(decoded.Description)
	if description == "" {
		description = http.StatusText(response.StatusCode)
	}
	description = c.redact(description)

	switch {
	case response.StatusCode == http.StatusOK && decoded.OK:
		return attemptResult{}
	case response.StatusCode == http.StatusTooManyRequests:
		retryAfter := time.Duration(decoded.Parameters.RetryAfter) * time.Second
		if retryAfter <= 0 {
			retryAfter = time.Second
		}
		return attemptResult{
			err:         &RetryAfterError{RetryAfter: retryAfter, Description: description},
			retry:       true,
			retryAfter:  retryAfter,
			description: description,
		}
	case response.StatusCode >= 500 || response.StatusCode == http.StatusRequestTimeout:
		return attemptResult{
			err:   &APIError{StatusCode: response.StatusCode, Description: description},
			retry: true,
		}
	case response.StatusCode == http.StatusOK && !decoded.OK:
		if decodeErr != nil {
			return attemptResult{err: &APIError{StatusCode: response.StatusCode, Description: "unexpected non-JSON response"}}
		}
		return attemptResult{err: &APIError{StatusCode: response.StatusCode, Description: description}}
	case readErr != nil:
		return attemptResult{err: &TransportError{message: "read telegram response: " + c.redact(readErr.Error())}, retry: true}
	default:
		return attemptResult{err: &APIError{StatusCode: response.StatusCode, Description: description}}
	}
}

func (c *Client) transportError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return &TransportError{message: "telegram request failed: " + ctxErr.Error()}
	}
	return &TransportError{message: "telegram request failed: " + c.redact(err.Error())}
}

// redact removes the bot token from a piece of text. The token is part of the
// URL of every request, so a *url.Error would otherwise print it.
func (c *Client) redact(text string) string {
	if c == nil || c.token == "" {
		return text
	}
	return strings.ReplaceAll(text, c.token, "[REDACTADO]")
}

// fits reports whether wait plus the reserve for persisting the state remains
// inside the context deadline.
func (c *Client) fits(ctx context.Context, wait time.Duration) bool {
	deadline, ok := ctx.Deadline()
	if !ok {
		return true
	}
	return time.Until(deadline) > wait+c.minSendBudget
}

func (c *Client) backoff(attempt int) time.Duration {
	wait := c.initialBackoff
	for i := 1; i < attempt; i++ {
		wait *= 2
		if wait >= c.maxBackoff {
			return c.maxBackoff
		}
	}
	if wait > c.maxBackoff {
		return c.maxBackoff
	}
	return wait
}

func sleepContext(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
