package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	var err bytes.Buffer

	exitCode := Run([]string{"nope"}, &out, &err)
	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code")
	}
	if !strings.Contains(err.String(), "unknown command") {
		t.Fatalf("expected unknown command message, got %q", err.String())
	}
}

func TestRunNoArgs(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	exitCode := Run(nil, &out, &errOut)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "usage") {
		t.Fatalf("expected usage on stdout, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", errOut.String())
	}
}

func TestRunHelp(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer

	exitCode := Run([]string{"help"}, &out, &errOut)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(out.String(), "usage") {
		t.Fatalf("expected usage on stdout, got %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", errOut.String())
	}
}
