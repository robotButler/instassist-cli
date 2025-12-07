# ✨ insta-assist

A beautiful, fast TUI (Terminal User Interface) for getting instant AI-powered command suggestions. Designed for quick popup usage with keyboard shortcuts.

![Version](https://img.shields.io/badge/version-1.0.0-blue)
![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)
![License](https://img.shields.io/badge/license-MIT-green)

## Features

- **Fast & Lightweight**: Minimal TUI optimized for quick interactions
- **AI-Powered**: Get command suggestions from `codex`, `claude`, `gemini`, or `opencode` CLIs
- **Beautiful UI**: Color-coded interface with intuitive navigation
- **Flexible Output**: Copy to clipboard, execute directly, or output to stdout
- **Keyboard-Driven**: Fully keyboard navigable for maximum efficiency
- **Non-Interactive Mode**: Use via CLI for scripting and automation
- **Popup-Friendly**: Perfect for launching with desktop keyboard shortcuts

## Prerequisites

### Required

You need at least one of these AI CLIs installed:
- [codex](https://github.com/anthropics/anthropic-tools) - Anthropic's codex CLI
- [claude](https://github.com/anthropics/claude-cli) - Claude CLI
- [gemini](https://github.com/google/generative-ai-cli) - Google Gemini CLI
- [opencode](https://github.com/opencodedev/opencode) - OpenCode CLI

### Clipboard Support

For clipboard functionality, you need:
- **Linux**: Install `xclip` or `xsel`
  ```bash
  # Arch Linux
  sudo pacman -S xclip
  # or
  sudo pacman -S xsel

  # Debian/Ubuntu
  sudo apt install xclip
  # or
  sudo apt install xsel
  ```
- **macOS**: Works out of the box (uses built-in `pbcopy`)
- **Windows**: Works out of the box

## Installation

### Quick Install (Recommended)

```bash
make install
```
This will:
1. Build the binary
2. Install it to `/usr/local/bin/insta`
3. Copy the schema file to `/usr/local/share/insta-assist/`

### Manual Build

```bash
# Build only
make build
# or
go build -o insta .

# Run from current directory
./insta
```

### Uninstall

```bash
make uninstall
```

## Usage

### Interactive TUI Mode

Launch the interactive interface:

```bash
insta
```

Or specify a default CLI:

```bash
insta -cli claude
```

### Keyboard Shortcuts

#### Input Mode
- `Enter` - Send prompt to AI
- `Ctrl+R` - Send prompt and auto-execute first result
- `Ctrl+N` / `Ctrl+P` - Switch CLI
- `Alt+Enter` or `Ctrl+J` - Insert newline
- `Ctrl+C` or `Esc` - Quit

#### Viewing Mode (Results)
- `Up/Down` or `j/k` - Navigate options
- `Enter` - Copy selected option to clipboard and exit
- `Ctrl+R` - Execute selected option and exit
- `Alt+Enter` - Start new prompt
- `Ctrl+N` / `Ctrl+P` - Switch CLI
- `Ctrl+C`, `Esc`, or `q` - Quit without action

### CLI Mode (Non-Interactive)

Perfect for scripting and automation:

```bash
# Send prompt and copy first option to clipboard
insta -prompt "list files in current directory"

# Send prompt and output to stdout
insta -prompt "list files" -output stdout

# Execute the first option directly
insta -prompt "create a backup directory" -output exec

# Select specific option (0-based index)
insta -prompt "git commands" -select 0 -output stdout

# Read from stdin
echo "show disk usage" | insta -output stdout

# Use with specific CLI
insta -cli codex -prompt "docker commands"
insta -cli gemini -prompt "use rsync"
insta -cli opencode -prompt "write a kubectl one-liner"
```

### CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-cli` | `codex` | Choose AI CLI: `codex`, `claude`, `gemini`, or `opencode` |
| `-prompt` | - | Prompt for non-interactive mode |
| `-select` | `-1` | Auto-select option by index (0-based, -1 = first) |
| `-output` | `clipboard` | Output mode: `clipboard`, `stdout`, or `exec` |
| `-version` | - | Print version and exit |

## Desktop Integration

### Linux (GNOME/KDE)

Create a keyboard shortcut that runs:

```bash
# For terminal emulator popup
gnome-terminal --geometry=100x30 -- insta

# Or with kitty
kitty --title "insta-assist" --override initial_window_width=1000 --override initial_window_height=600 insta

# Or with alacritty
alacritty --title "insta-assist" -e insta
```

Bind to a key like `Super+Space` or `Ctrl+Alt+A`.

### macOS

Create an automator Quick Action or use Hammerspoon:

```lua
-- Hammerspoon config
hs.hotkey.bind({"cmd", "ctrl"}, "space", function()
    hs.execute("/usr/local/bin/alacritty -e insta")
end)
```

### i3/sway

Add to your config:

```
# i3 config
bindsym $mod+space exec alacritty --class floating -e insta
for_window [class="floating"] floating enable

# sway config
bindsym $mod+space exec alacritty --class floating -e insta
for_window [app_id="floating"] floating enable
```

## How It Works

1. You enter a prompt describing what you want to do
2. insta-assist sends it to your chosen AI CLI (codex, claude, gemini, or opencode) with a JSON schema
3. The AI returns structured options with descriptions
4. You select an option and choose to copy it or run it directly
5. The app exits, ready for your next quick query

## Examples

**Prompt:** "git commit with message"
```
Options:
▶ git commit -m "message"
  Create a commit with inline message

  git commit
  Open editor for commit message
```

**Prompt:** "compress images in current dir"
```
Options:
▶ find . -name "*.jpg" -exec convert {} -quality 85 {} \;
  Compress all JPG files to 85% quality

  mogrify -quality 85 *.jpg
  Compress using ImageMagick mogrify
```

## Development

### Build & Test

```bash
# Show all make targets
make help

# Build
make build

# Test
make test

# Run
make run

# Clean
make clean
```

### Project Structure

```
insta-assist/
├── main.go             # Flags and entrypoint routing
├── ui.go               # Bubble Tea model, rendering, key handling
├── noninteractive.go   # CLI-only execution flow
├── prompt.go           # Prompt building, schema resolution, JSON parsing
├── options.schema.json # JSON schema for AI responses
├── Makefile            # Build and installation
├── README.md           # Documentation
├── go.mod              # Go dependencies (Go 1.24.x)
└── go.sum              # Dependency checksums
```

## Configuration

The app looks for `options.schema.json` in these locations (in order):
1. Same directory as the binary
2. Current working directory
3. `/usr/local/share/insta-assist/`

## Troubleshooting

**"schema not found" error**
- Run `make install` to copy schema to system location
- Or keep `options.schema.json` in the same directory as the binary

**AI CLI not found**
- Make sure one of the supported AI CLIs is installed and in your PATH: `codex`, `claude`, `gemini`, or `opencode`
- Test with `codex --version`, `claude --version`, `gemini --version`, or `opencode --version`

**Clipboard not working**
- **Linux**: Make sure `xclip` or `xsel` is installed
  ```bash
  # Test if xclip is available
  which xclip
  # or
  which xsel
  ```
- If clipboard fails, you can use CLI mode with `-output stdout` instead:
  ```bash
  insta -prompt "your prompt" -output stdout
  ```

**Colors not showing**
- Ensure your terminal supports 256 colors
- Try `echo $TERM` - should be `xterm-256color` or similar

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

MIT License - see LICENSE file for details

## Credits

Built with:
- [Bubble Tea](https://github.com/charmbracelet/bubbletea) - TUI framework
- [Lipgloss](https://github.com/charmbracelet/lipgloss) - Styling
- [Bubbles](https://github.com/charmbracelet/bubbles) - TUI components

## Roadmap

- [ ] Custom keybindings configuration
- [ ] History of previous prompts
- [ ] Multiple AI provider support
- [ ] Custom prompt templates
- [ ] Configuration file support
- [ ] Shell completion scripts

---

**Made with ❤️ for terminal enthusiasts**
