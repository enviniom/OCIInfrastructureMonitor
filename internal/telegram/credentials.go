// Package telegram delivers messages to the official Telegram Bot API. It
// keeps the credentials out of every log and error: the bot token is part of
// the request URL, so transport errors are scrubbed before they are returned.
package telegram

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// DefaultEnvPath is the default location of the Telegram credentials file.
const DefaultEnvPath = "/etc/oci-infrastructure-monitor/telegram.env"

// Environment variable names accepted by the credentials loader.
const (
	EnvBotToken = "TELEGRAM_BOT_TOKEN"
	EnvChatID   = "TELEGRAM_CHAT_ID"
)

// Credentials are the two values needed to send a message. They are never
// serialized, logged or accepted as command-line arguments.
type Credentials struct {
	BotToken string
	ChatID   string
}

// Validate checks that both credentials are present and usable.
func (c Credentials) Validate() error {
	if strings.TrimSpace(c.BotToken) == "" {
		return errors.New("TELEGRAM_BOT_TOKEN is missing")
	}
	if strings.ContainsAny(c.BotToken, " \t\r\n") {
		return errors.New("TELEGRAM_BOT_TOKEN contains whitespace")
	}
	if strings.TrimSpace(c.ChatID) == "" {
		return errors.New("TELEGRAM_CHAT_ID is missing")
	}
	return nil
}

// LoadCredentials reads path and the process environment. Values in the
// environment override non-empty values from the file, which keeps a manual
// override possible without editing the installed file. A missing file is not
// an error when both environment variables are set.
func LoadCredentials(path string) (Credentials, error) {
	return LoadCredentialsWithLookup(path, os.LookupEnv)
}

// LoadCredentialsWithLookup is LoadCredentials with an injectable environment,
// used by tests.
func LoadCredentialsWithLookup(path string, lookup func(string) (string, bool)) (Credentials, error) {
	values := map[string]string{}
	if path != "" {
		fileValues, err := readEnvFile(path)
		if err != nil {
			return Credentials{}, err
		}
		values = fileValues
	}
	if lookup != nil {
		for _, key := range []string{EnvBotToken, EnvChatID} {
			if value, ok := lookup(key); ok && strings.TrimSpace(value) != "" {
				values[key] = value
			}
		}
	}

	credentials := Credentials{
		BotToken: strings.TrimSpace(values[EnvBotToken]),
		ChatID:   strings.TrimSpace(values[EnvChatID]),
	}
	if err := credentials.Validate(); err != nil {
		return Credentials{}, fmt.Errorf("telegram credentials: %w", err)
	}
	return credentials, nil
}

// readEnvFile parses a KEY=VALUE file and enforces restrictive permissions. The
// returned values never appear in an error message.
func readEnvFile(path string) (map[string]string, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat telegram credentials %q: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("telegram credentials %q is a directory", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("telegram credentials %q must not be readable by group or others (mode %04o, want 0600)", path, perm)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open telegram credentials %q: %w", path, err)
	}
	defer f.Close()

	values, err := ParseEnv(f)
	if err != nil {
		return nil, fmt.Errorf("telegram credentials %q: %w", path, err)
	}
	return values, nil
}

// ParseEnv parses the simple KEY=VALUE format of telegram.env. Empty lines and
// lines starting with # are ignored, an optional "export " prefix is accepted,
// and matching single or double quotes around a value are removed. Unknown keys
// are kept out of the result: only the two known credentials are returned.
func ParseEnv(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024), 64*1024)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d has no =", lineNumber)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d has an empty key", lineNumber)
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		if key == EnvBotToken || key == EnvChatID {
			values[key] = value
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read credentials: %w", err)
	}
	return values, nil
}
