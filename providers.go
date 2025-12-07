package main

import (
	"context"
	"os/exec"
	"strings"
)

// runClaude attempts to call the Claude CLI with schema support, and falls back
// to a plain JSON response if the installed CLI doesn't support --json-schema.
func runClaude(ctx context.Context, prompt, schemaPath string) ([]byte, error) {
	attempt := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "claude", args...)
		return cmd.CombinedOutput()
	}

	args := []string{"-p", prompt, "--json-schema", schemaPath}
	out, err := attempt(args...)
	if err == nil {
		return out, nil
	}

	// Try fallback if schema flag is unknown or rejected.
	lowerOut := strings.ToLower(string(out))
	if strings.Contains(lowerOut, "unknown flag") ||
		strings.Contains(lowerOut, "flag provided but not defined") ||
		strings.Contains(lowerOut, "no such option") ||
		strings.Contains(lowerOut, "unrecognized option") {
		return attempt("-p", prompt, "--json")
	}

	// If schema-related error is likely, still try the json fallback once.
	if strings.Contains(lowerOut, "schema") || strings.Contains(lowerOut, "json-schema") {
		if fallbackOut, fallbackErr := attempt("-p", prompt, "--json"); fallbackErr == nil {
			return fallbackOut, nil
		}
	}

	return out, err
}
