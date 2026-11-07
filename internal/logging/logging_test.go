package logging

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug": LevelDebug,
		"INFO":  LevelInfo,
		"warn":  LevelWarn,
		"error": LevelError,
	}
	for name, want := range cases {
		got, err := ParseLevel(name)
		if err != nil {
			t.Fatalf("ParseLevel(%q) error = %v", name, err)
		}
		if got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", name, got, want)
		}
	}
	if _, err := ParseLevel("trace"); err == nil {
		t.Fatal("ParseLevel(trace) error = nil")
	}
}

func TestLoggerFiltersByLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(LevelWarn, &buf)
	logger.now = func() time.Time { return time.Date(2026, 11, 7, 13, 30, 0, 0, time.UTC) }

	logger.Debugf("depuración")
	logger.Infof("información")
	logger.Warnf("atención")
	logger.Errorf("error")

	out := buf.String()
	if strings.Contains(out, "depuración") || strings.Contains(out, "información") {
		t.Errorf("logger wrote below its level:\n%s", out)
	}
	if !strings.Contains(out, "2026-11-07T13:30:00Z warn  atención") {
		t.Errorf("missing warn line:\n%s", out)
	}
	if !strings.Contains(out, "2026-11-07T13:30:00Z error error") {
		t.Errorf("missing error line:\n%s", out)
	}
}

func TestLoggerIsConcurrencySafe(t *testing.T) {
	var buf bytes.Buffer
	logger := New(LevelDebug, &buf)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Infof("línea")
		}()
	}
	wg.Wait()
	if got := strings.Count(buf.String(), "línea"); got != 20 {
		t.Errorf("lines = %d, want 20", got)
	}
}

func TestNilLoggerDiscards(t *testing.T) {
	var logger *Logger
	logger.Infof("nada")
	logger.Errorf("nada")
	if logger.Level() != LevelInfo {
		t.Errorf("nil Level() = %v", logger.Level())
	}
}
