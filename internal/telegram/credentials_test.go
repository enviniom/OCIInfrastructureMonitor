package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnv(t *testing.T) {
	input := `
# comentario
export TELEGRAM_BOT_TOKEN="123456:secret"
TELEGRAM_CHAT_ID = '-100123'
IGNORED=value
`
	values, err := ParseEnv(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseEnv() error = %v", err)
	}
	if got := values[EnvBotToken]; got != "123456:secret" {
		t.Errorf("token = %q", got)
	}
	if got := values[EnvChatID]; got != "-100123" {
		t.Errorf("chat id = %q", got)
	}
	if _, ok := values["IGNORED"]; ok {
		t.Error("unknown key was returned")
	}
}

func TestParseEnvRejectsMalformedLine(t *testing.T) {
	if _, err := ParseEnv(strings.NewReader("TELEGRAM_BOT_TOKEN\n")); err == nil {
		t.Fatal("ParseEnv() error = nil, want an error")
	}
}

func writeCredentials(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "telegram.env")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadCredentialsFromFile(t *testing.T) {
	path := writeCredentials(t, "TELEGRAM_BOT_TOKEN=123:abc\nTELEGRAM_CHAT_ID=42\n", 0o600)
	credentials, err := LoadCredentialsWithLookup(path, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("LoadCredentialsWithLookup() error = %v", err)
	}
	if credentials.BotToken != "123:abc" || credentials.ChatID != "42" {
		t.Errorf("credentials = %+v", credentials)
	}
}

func TestLoadCredentialsEnvironmentOverridesFile(t *testing.T) {
	path := writeCredentials(t, "TELEGRAM_BOT_TOKEN=from-file\nTELEGRAM_CHAT_ID=1\n", 0o600)
	lookup := func(key string) (string, bool) {
		switch key {
		case EnvBotToken:
			return "from-env", true
		case EnvChatID:
			return "", true
		default:
			return "", false
		}
	}
	credentials, err := LoadCredentialsWithLookup(path, lookup)
	if err != nil {
		t.Fatalf("LoadCredentialsWithLookup() error = %v", err)
	}
	if credentials.BotToken != "from-env" {
		t.Errorf("token = %q, want the environment value", credentials.BotToken)
	}
	if credentials.ChatID != "1" {
		t.Errorf("chat id = %q, want the file value", credentials.ChatID)
	}
}

func TestLoadCredentialsRejectsInsecurePermissions(t *testing.T) {
	path := writeCredentials(t, "TELEGRAM_BOT_TOKEN=123:abc\nTELEGRAM_CHAT_ID=42\n", 0o644)
	_, err := LoadCredentialsWithLookup(path, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("LoadCredentialsWithLookup() error = nil, want a permissions error")
	}
	if !strings.Contains(err.Error(), "0600") {
		t.Errorf("error = %v, want a mention of the required mode", err)
	}
}

func TestLoadCredentialsMissingFileAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.env")
	_, err := LoadCredentialsWithLookup(path, func(string) (string, bool) { return "", false })
	if err == nil {
		t.Fatal("LoadCredentialsWithLookup() error = nil")
	}
	if strings.Contains(err.Error(), "missing.env") && strings.Contains(err.Error(), "123:") {
		t.Errorf("error leaked credentials: %v", err)
	}
}

func TestLoadCredentialsEnvironmentOnly(t *testing.T) {
	lookup := func(key string) (string, bool) {
		switch key {
		case EnvBotToken:
			return "123:abc", true
		case EnvChatID:
			return "42", true
		default:
			return "", false
		}
	}
	credentials, err := LoadCredentialsWithLookup(filepath.Join(t.TempDir(), "missing.env"), lookup)
	if err != nil {
		t.Fatalf("LoadCredentialsWithLookup() error = %v", err)
	}
	if credentials.BotToken != "123:abc" || credentials.ChatID != "42" {
		t.Errorf("credentials = %+v", credentials)
	}
}

func TestCredentialsValidate(t *testing.T) {
	cases := []Credentials{
		{},
		{BotToken: "123:abc"},
		{ChatID: "42"},
		{BotToken: "123:abc def", ChatID: "42"},
	}
	for _, credentials := range cases {
		if err := credentials.Validate(); err == nil {
			t.Errorf("Validate(%+v) error = nil", credentials)
		}
	}
}
