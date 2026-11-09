package main

import "testing"

func TestExecuteRejectsExtraArgs(t *testing.T) {
	cases := [][]string{
		{"run", "x"},
		{"check", "x"},
		{"status", "x"},
		{"report", "x"},
		{"test-telegram", "x"},
		{"version", "x"},
		{"help", "x"},
	}
	for _, args := range cases {
		if got := execute(args); got != 2 {
			t.Errorf("execute(%v) = %d, want 2", args, got)
		}
	}
}

func TestExecuteHelpExitsZero(t *testing.T) {
	cases := [][]string{
		{"help"},
		{"--help"},
		{"-h"},
		{"run", "--help"},
		{"check", "--help"},
		{"status", "--help"},
		{"report", "--help"},
		{"test-telegram", "--help"},
	}
	for _, args := range cases {
		if got := execute(args); got != 0 {
			t.Errorf("execute(%v) = %d, want 0", args, got)
		}
	}
}

func TestExecuteVersionAliases(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-version"}} {
		if got := execute(args); got != 0 {
			t.Errorf("execute(%v) = %d, want 0", args, got)
		}
	}
}

func TestExecuteUnknownCommand(t *testing.T) {
	if got := execute([]string{"nope"}); got != 2 {
		t.Errorf("execute(nope) = %d, want 2", got)
	}
}
