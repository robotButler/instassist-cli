package instassist

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atotto/clipboard"
)

func runNonInteractive(cliName, userPrompt string, selectIndex int, outputMode string) {
	schemaPath, schemaJSON, err := schemaSources()
	if err != nil {
		log.Fatalf("schema not found: %v", err)
	}

	fullPrompt := buildPrompt(userPrompt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var output []byte
	switch strings.ToLower(cliName) {
	case "codex":
		cmd := exec.CommandContext(ctx, "codex", "exec", "--output-schema", schemaPath, "--skip-git-repo-check")
		cmd.Stdin = strings.NewReader(fullPrompt)
		output, err = cmd.CombinedOutput()
	case "claude":
		cmd := exec.CommandContext(ctx, "claude", "-p", fullPrompt, "--print", "--output-format", "json", "--json-schema", schemaJSON)
		output, err = cmd.CombinedOutput()
	case "gemini":
		cmd := exec.CommandContext(ctx, "gemini", "--output-format", "json", fullPrompt)
		output, err = cmd.CombinedOutput()
	case "opencode":
		cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", fullPrompt)
		output, err = cmd.CombinedOutput()
	default:
		log.Fatalf("unknown CLI: %s (supported: codex, claude, gemini, opencode)", cliName)
	}

	if err != nil {
		log.Fatalf("CLI error: %v\nOutput: %s", err, string(output))
	}

	opts, parseErr := extractOptions(string(output))
	if parseErr != nil {
		log.Fatalf("parse error: %v\nRaw output: %s", parseErr, string(output))
	}

	if len(opts) == 0 {
		log.Fatalf("no options returned")
	}

	var selectedValue string
	if selectIndex >= 0 && selectIndex < len(opts) {
		selectedValue = opts[selectIndex].Value
	} else {
		selectedValue = opts[0].Value
	}

	switch strings.ToLower(outputMode) {
	case "stdout":
		fmt.Println(selectedValue)
	case "exec":
		cmd := exec.Command("sh", "-c", selectedValue)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			log.Fatalf("exec error: %v", err)
		}
	case "clipboard":
		if err := clipboard.WriteAll(selectedValue); err != nil {
			log.Fatalf("clipboard error: %v\nHint: On Linux, install xclip or xsel (e.g., 'sudo pacman -S xclip')", err)
		}
		fmt.Printf("✅ Copied to clipboard: %s\n", selectedValue)
	default:
		log.Fatalf("unknown output mode: %s", outputMode)
	}
}

func cliAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
