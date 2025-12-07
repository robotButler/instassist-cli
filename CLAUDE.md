# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**instassist** is a TUI (Terminal User Interface) application for getting instant AI-powered command suggestions. It's designed for quick popup usage with desktop keyboard shortcuts and supports both interactive TUI mode and non-interactive CLI mode.

The application acts as a frontend that sends user prompts to AI CLIs (`codex`, `claude`, `gemini`, `opencode`), receives structured JSON responses, and presents options in a colorful Bubble Tea interface.

## Build Commands

```bash
# Build the binary
make build

# Build and install system-wide (requires sudo)
make install

# Run in interactive mode
make run

# Quick test (version and help flags)
make test

# Clean build artifacts
make clean

# Remove system installation
make uninstall

# See all available targets
make help
```

## Architecture

### Dual Operating Modes

The application has two distinct execution paths determined in `main()`:

1. **Non-interactive CLI mode** (`runNonInteractive`): Activated when `-prompt` flag is provided or stdin is piped. Runs a single request, outputs result (clipboard/stdout/exec), and exits.

2. **Interactive TUI mode** (`newModel` + Bubble Tea): Full-screen terminal UI with state management through the Bubble Tea framework.

### State Machine (TUI Mode)

The TUI operates as a state machine with three modes (`viewMode` enum):

- `modeInput`: User enters prompt (textarea component active)
- `modeRunning`: Request sent to AI CLI, waiting for response
- `modeViewing`: Displaying results and navigating options

State transitions:
- Input → Running: User presses Enter or Ctrl+R with non-empty prompt
- Running → Viewing: AI response received (success or error)
- Viewing → Input: User presses Alt+Enter/Ctrl+J for new prompt
- Viewing → Exit: User presses Enter (copy) or Ctrl+R (execute)

### AI CLI Integration

The app supports four AI CLIs with different invocation patterns:

**codex**: Uses stdin for prompt input
```go
cmd := exec.CommandContext(ctx, "codex", "exec", "--output-schema", schemaPath)
cmd.Stdin = strings.NewReader(fullPrompt)
```

**claude**: Uses command-line flag for prompt
```go
cmd := exec.CommandContext(ctx, "claude", "-p", fullPrompt, "--json-schema", schemaPath)
```

**gemini**: Positional prompt with JSON output
```go
cmd := exec.CommandContext(ctx, "gemini", "--output-format", "json", fullPrompt)
```

**opencode**: Uses `run` with JSON format
```go
cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", fullPrompt)
```

Both receive the same structured prompt (via `buildPrompt()`) that includes:
1. User's natural language request
2. Instructions to respond with specific JSON schema

### JSON Response Parsing

The `parseOptions()` function is resilient to AI output that may include extra text:
- Searches for all occurrences of `{"options"` in the response
- Tries to decode each as JSON
- Returns the **last valid** JSON object found (handles cases where AI includes multiple attempts)
- Sorts options by `recommendation_order` field (lower = higher priority)

### Schema File Resolution

`optionsSchemaPath()` searches in order:
1. Same directory as the executable binary
2. Current working directory
3. `/usr/local/share/insta-assist/options.schema.json` (system install location)

This allows both development (run from source) and production (installed) use cases.

## Key Design Patterns

### Lipgloss Styling

The UI heavily uses `lipgloss.NewStyle()` for consistent visual theming:
- Styles are created inline in the `View()` and `renderOptionsTable()` functions
- Colors use numbered palette (e.g., "205" for purple, "62" for highlights)
- Selected options use background color + foreground color + arrow prefix

### Keyboard Input Handling

Special key combinations are handled with helper functions:
- `isNewline()`: Alt+Enter or Ctrl+J (for adding newlines)
- `isCtrlEnter()`: Ctrl+Enter (also submits prompt)
- `isCtrlR()`: Ctrl+R (submit + auto-execute)

The `handleKeyMsg()` dispatcher routes to mode-specific handlers:
- `handleInputKeys()`: Text entry, submission (Enter/Ctrl+Enter), CLI switching (`ctrl+n` / `ctrl+p`)
- `handleRunningKeys()`: Minimal (only CLI switching)
- `handleViewingKeys()`: Navigation, copy/execute, CLI switching, and reset to input

### Version Embedding

Version is embedded at build time via ldflags:
```bash
go build -ldflags "-X main.version=$(VERSION)" -o insta .
```

The `version` constant is declared but set by the linker.

## Testing in Development

To test the TUI without installing system-wide, ensure `options.schema.json` is in the working directory:

```bash
make build
./insta  # TUI mode

# CLI mode examples
./insta -prompt "list files" -output stdout
echo "git commands" | ./insta -output stdout
```

## Adding New AI CLI Support

To add support for a new AI CLI:

1. Add entry to `cliOptions` slice in `newModel()` with appropriate `runPrompt` function
2. Add corresponding case in `runNonInteractive()` for CLI mode support
3. Ensure the CLI accepts the schema file path and returns JSON matching the schema

## Important File Relationships

- `main.go`: Flag parsing and entrypoint routing (non-interactive vs TUI)
- `ui.go`: Bubble Tea model, view, and key handling
- `noninteractive.go`: CLI-only execution flow
- `prompt.go`: Prompt construction, schema resolution, and JSON parsing
- `options.schema.json`: JSON schema sent to AI CLIs, defines response structure
- `Makefile`: Build automation with version embedding
- Schema file must be accessible at runtime (see Schema File Resolution above)
