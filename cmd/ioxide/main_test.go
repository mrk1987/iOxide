package main

import (
	"strings"
	"testing"
)

func TestRunWithoutArgumentsPrintsUsage(t *testing.T) {
	if err := run(nil); err != nil {
		t.Errorf("run() with no arguments = %v, want nil (usage)", err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Errorf("run(help) = %v, want nil", err)
	}
}

func TestRunRejectsBadInput(t *testing.T) {
	cases := [][]string{
		{"bogus"},
		{"auth"},
		{"auth", "a", "b"},
		{"import-session"},
		{"-tls-cert", "only-cert", "serve"},
	}
	for _, args := range cases {
		if err := run(args); err == nil {
			t.Errorf("run(%v) = nil, want error", args)
		}
	}
}

func TestUsageMentionsEveryCommand(t *testing.T) {
	for _, cmd := range []string{"auth", "import-session", "status", "serve"} {
		if !strings.Contains(usage, "  "+cmd) {
			t.Errorf("usage does not mention %q", cmd)
		}
	}
}
