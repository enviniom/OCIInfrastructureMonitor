package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCommandNetworkDisabled(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "state")
	configPath := writeRunConfig(t, stateDir, false, false)

	var code int
	out, errOut := captureOutput(t, func() {
		code = execute([]string{"check", "--config", configPath})
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "network.enabled=false") {
		t.Errorf("output = %q", out)
	}
}
