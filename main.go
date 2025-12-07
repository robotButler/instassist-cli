package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type cliOption struct {
	name      string
	runPrompt func(ctx context.Context, prompt string) ([]byte, error)
}

const (
	version        = "1.0.0"
	defaultCLIName = "codex"
	titleText      = "insta-assist"

	helpInput   = "enter: send • ctrl+r: send & run • shift/alt+enter: newline"
	helpViewing = "up/down/j/k: select • enter: copy & exit • ctrl+r: run & exit • alt+enter: new prompt • esc/q: quit"
)

type viewMode int

const (
	modeInput viewMode = iota
	modeRunning
	modeViewing
)

type responseMsg struct {
	output []byte
	err    error
	cli    string
}

type optionEntry struct {
	Value               string `json:"value"`
	Description         string `json:"description"`
	RecommendationOrder int    `json:"recommendation_order"`
}

type optionResponse struct {
	Options []optionEntry `json:"options"`
}

type model struct {
	cliOptions []cliOption
	cliIndex   int

	input textarea.Model

	mode    viewMode
	running bool

	width  int
	height int
	ready  bool

	lastPrompt string
	status     string

	rawOutput string

	options        []optionEntry
	selected       int
	lastParseError error

	autoExecute bool // if true, execute first result and exit
}

func main() {
	cliFlag := flag.String("cli", defaultCLIName, "default CLI to use: codex or claude")
	promptFlag := flag.String("prompt", "", "prompt to send (non-interactive mode)")
	selectFlag := flag.Int("select", -1, "auto-select option by index (0-based, use with -prompt)")
	outputFlag := flag.String("output", "clipboard", "output mode: clipboard, stdout, or exec")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("insta-assist version %s\n", version)
		os.Exit(0)
	}

	// Non-interactive mode
	if *promptFlag != "" {
		runNonInteractive(*cliFlag, *promptFlag, *selectFlag, *outputFlag)
		return
	}

	// Check if stdin is not a terminal (piped input)
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Fatalf("error reading stdin: %v", err)
		}
		prompt := strings.TrimSpace(string(data))
		if prompt != "" {
			runNonInteractive(*cliFlag, prompt, *selectFlag, *outputFlag)
			return
		}
	}

	// Interactive TUI mode
	m := newModel(*cliFlag)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func runNonInteractive(cliName, userPrompt string, selectIndex int, outputMode string) {
	schemaPath, err := optionsSchemaPath()
	if err != nil {
		log.Fatalf("schema not found: %v", err)
	}

	fullPrompt := buildPrompt(userPrompt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	var output []byte
	switch strings.ToLower(cliName) {
	case "codex":
		cmd := exec.CommandContext(ctx, "codex", "exec", "--output-schema", schemaPath)
		cmd.Stdin = strings.NewReader(fullPrompt)
		output, err = cmd.CombinedOutput()
	case "claude":
		cmd := exec.CommandContext(ctx, "claude", "-p", fullPrompt, "--json-schema", schemaPath)
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

	opts, parseErr := parseOptions(string(output))
	if parseErr != nil {
		log.Fatalf("parse error: %v\nRaw output: %s", parseErr, string(output))
	}

	if len(opts) == 0 {
		log.Fatalf("no options returned")
	}

	// Select the option
	var selectedValue string
	if selectIndex >= 0 && selectIndex < len(opts) {
		selectedValue = opts[selectIndex].Value
	} else {
		selectedValue = opts[0].Value
	}

	// Handle output mode
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

func newModel(defaultCLI string) model {
	schemaPath, err := optionsSchemaPath()
	if err != nil {
		log.Fatalf("schema not found: %v", err)
	}

	// Define all possible CLI options
	allCLIOptions := []cliOption{
		{
			name: "codex",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "codex", "exec", "--output-schema", schemaPath)
				cmd.Stdin = strings.NewReader(prompt)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "claude",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "claude", "-p", prompt, "--json-schema", schemaPath)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "gemini",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				// gemini uses positional prompt and can output JSON
				cmd := exec.CommandContext(ctx, "gemini", "--output-format", "json", prompt)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "opencode",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				// opencode uses "run" command with --format json
				cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", prompt)
				return cmd.CombinedOutput()
			},
		},
	}

	// Filter to only available CLIs
	var cliOptions []cliOption
	for _, opt := range allCLIOptions {
		if cliAvailable(opt.name) {
			cliOptions = append(cliOptions, opt)
		}
	}

	if len(cliOptions) == 0 {
		log.Fatalf("No AI CLIs found. Please install at least one of: codex, claude, gemini, opencode")
	}

	input := textarea.New()
	input.Placeholder = "Enter prompt"
	input.Focus()
	input.CharLimit = 0
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.SetHeight(1) // Start with 1 line, will expand dynamically

	cliIndex := 0
	for i, opt := range cliOptions {
		if strings.EqualFold(opt.name, defaultCLI) {
			cliIndex = i
			break
		}
	}

	return model{
		cliOptions: cliOptions,
		cliIndex:   cliIndex,
		input:      input,
		mode:       modeInput,
		status:     helpInput,
	}
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		m.resizeComponents()
		// Also adjust textarea height when window resizes
		if m.mode == modeInput {
			m.adjustTextareaHeight()
		}
		return m, nil
	case responseMsg:
		return m.handleResponse(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	default:
	}

	// Handle all other messages (including paste) in input mode
	if m.mode == modeInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		// Always adjust height after any update
		m.adjustTextareaHeight()
		return m, cmd
	}

	return m, nil
}

func (m model) handleResponse(msg responseMsg) (tea.Model, tea.Cmd) {
	m.running = false
	m.mode = modeViewing

	respText := strings.TrimSpace(string(msg.output))
	if msg.err != nil && respText == "" {
		respText = msg.err.Error()
	}
	m.rawOutput = respText
	m.lastParseError = nil

	if msg.err != nil {
		m.status = fmt.Sprintf("error from %s • %s", msg.cli, helpViewing)
		m.options = nil
		m.selected = 0
		return m, nil
	}

	opts, parseErr := parseOptions(respText)
	if parseErr != nil {
		m.lastParseError = parseErr
		m.status = fmt.Sprintf("parse error: %v • %s", parseErr, helpViewing)
		m.options = nil
		m.selected = 0
		return m, nil
	}

	m.options = opts
	m.selected = 0
	m.status = helpViewing

	// Auto-execute if requested (Ctrl+R from input mode)
	if m.autoExecute && len(opts) > 0 {
		value := opts[0].Value
		return m, tea.Sequence(
			tea.ExecProcess(exec.Command("sh", "-c", value), func(err error) tea.Msg {
				return tea.Quit()
			}),
		)
	}

	return m, nil
}

func (m model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeInput:
		return m.handleInputKeys(msg)
	case modeRunning:
		return m.handleRunningKeys(msg)
	case modeViewing:
		return m.handleViewingKeys(msg)
	default:
		return m, nil
	}
}

func (m model) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Always allow exit
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	// CLI switching with Ctrl-N (next) and Ctrl-P (previous)
	if msg.Type == tea.KeyCtrlN {
		m.nextCLI()
		return m, nil
	}
	if msg.Type == tea.KeyCtrlP {
		m.prevCLI()
		return m, nil
	}
	if isCtrlR(msg) {
		// Ctrl+R: Submit and auto-execute first result
		m.autoExecute = true
		return m.submitPrompt()
	}
	if isShiftEnter(msg) {
		// Shift/Alt+Enter: Add newline
		// Pre-emptively expand height BEFORE adding newline to prevent scrolling
		currentLines := strings.Count(m.input.Value(), "\n") + 1
		newLines := currentLines + 1 // We're about to add a newline
		if newLines <= 10 {
			m.input.SetHeight(newLines)
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return m, cmd
	}
	if msg.Type == tea.KeyEnter {
		// Plain Enter: Submit prompt
		m.autoExecute = false
		return m.submitPrompt()
	}
	if isCtrlEnter(msg) {
		// Ctrl+Enter: Also submit prompt
		m.autoExecute = false
		return m.submitPrompt()
	}
	// Other keys: Let textarea handle them
	return m.updateInput(msg)
}

func (m model) handleViewingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC || msg.String() == "esc" || msg.String() == "q":
		return m, tea.Quit
	case msg.String() == "ctrl+]":
		m.nextCLI()
		return m, nil
	case msg.String() == "ctrl+[":
		m.prevCLI()
		return m, nil
	case isShiftEnter(msg):
		m.mode = modeInput
		m.running = false
		m.input.SetValue("")
		m.input.Focus()
		m.status = helpInput
		m.options = nil
		m.lastParseError = nil
		m.rawOutput = ""
		m.autoExecute = false
		return m, nil
	case isCtrlR(msg):
		// Run command and exit
		value := m.selectedValue()
		if value == "" {
			if m.rawOutput == "" {
				m.status = "nothing to run • " + helpViewing
				return m, nil
			}
			value = m.rawOutput
		}
		return m, tea.Sequence(
			tea.ExecProcess(exec.Command("sh", "-c", value), func(err error) tea.Msg {
				return tea.Quit()
			}),
		)
	case msg.Type == tea.KeyEnter:
		value := m.selectedValue()
		if value == "" {
			if m.rawOutput == "" {
				m.status = "nothing to copy • " + helpViewing
				return m, nil
			}
			value = m.rawOutput
		}
		if err := clipboard.WriteAll(value); err != nil {
			m.status = fmt.Sprintf("❌ CLIPBOARD FAILED: %v • Install xclip/xsel on Linux • %s", err, helpViewing)
			return m, nil
		}
		// Successfully copied - show confirmation before exiting
		m.status = fmt.Sprintf("✅ Copied to clipboard: %s", value)
		return m, tea.Quit
	case msg.String() == "up" || msg.String() == "k":
		m.moveSelection(-1)
	case msg.String() == "down" || msg.String() == "j":
		m.moveSelection(1)
	}
	return m, nil
}

func (m model) handleRunningKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Always allow exit
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	// CLI switching with Ctrl-N (next) and Ctrl-P (previous)
	if msg.Type == tea.KeyCtrlN {
		m.nextCLI()
		return m, nil
	}
	if msg.Type == tea.KeyCtrlP {
		m.prevCLI()
		return m, nil
	}
	return m, nil
}

func (m *model) adjustTextareaHeight() {
	// Count actual newlines in content
	content := m.input.Value()
	lines := strings.Count(content, "\n") + 1
	if lines < 1 {
		lines = 1
	}

	// Add one extra line if we have multi-line content to prevent viewport scroll issues
	// The textarea viewport can scroll content when cursor is at end, so we need buffer
	if lines > 1 {
		lines = lines + 1
	}

	if lines > 20 {
		lines = 20 // Cap at 20 lines for reasonable UI
	}

	m.input.SetHeight(lines)
}

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	// For Enter keys, preemptively set height BEFORE update to prevent viewport scroll
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.Type == tea.KeyEnter {
			content := m.input.Value()
			futureLines := strings.Count(content, "\n") + 2 // +1 for the newline we're about to add, +1 for line count
			if futureLines > 1 {
				futureLines = futureLines + 1 // Add buffer
			}
			if futureLines > 20 {
				futureLines = 20
			}
			m.input.SetHeight(futureLines)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Always adjust height after any update
	m.adjustTextareaHeight()
	return m, cmd
}

func (m model) submitPrompt() (tea.Model, tea.Cmd) {
	userPrompt := strings.TrimRight(m.input.Value(), "\n")
	if strings.TrimSpace(userPrompt) == "" {
		m.status = "prompt is empty • " + helpInput
		return m, nil
	}

	m.lastPrompt = userPrompt
	fullPrompt := buildPrompt(userPrompt)
	m.running = true
	m.mode = modeRunning
	m.status = fmt.Sprintf("running %s… • tab: switch cli", m.currentCLI().name)
	m.options = nil
	m.lastParseError = nil
	m.rawOutput = ""
	m.selected = 0

	selectedCLI := m.currentCLI()
	cliName := selectedCLI.name
	cmd := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		out, err := selectedCLI.runPrompt(ctx, fullPrompt)
		return responseMsg{
			output: out,
			err:    err,
			cli:    cliName,
		}
	}

	m.resizeComponents()
	return m, cmd
}

func (m *model) nextCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex + 1) % len(m.cliOptions)
}

func (m *model) prevCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex - 1 + len(m.cliOptions)) % len(m.cliOptions)
}

func (m model) currentCLI() cliOption {
	return m.cliOptions[m.cliIndex]
}

func (m *model) resizeComponents() {
	if !m.ready {
		return
	}

	if m.width > 10 {
		// Account for: emoji (3) + border (2) + padding (2) + scroll indicator (2) + margin (1)
		m.input.SetWidth(m.width - 10)
	}
}

func isShiftEnter(msg tea.KeyMsg) bool {
	// Ctrl+J is the most reliable way to insert newline
	if msg.Type == tea.KeyCtrlJ {
		return true
	}
	// Alt+Enter works in most terminals
	if msg.Type == tea.KeyEnter && msg.Alt {
		return true
	}
	// Some terminals send these string representations
	s := msg.String()
	if s == "shift+enter" || s == "alt+enter" {
		return true
	}
	// Note: Shift+Enter often doesn't work because many terminals
	// can't distinguish it from plain Enter. Use Alt+Enter or Ctrl+J instead.
	return false
}

func isCtrlR(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlR || msg.String() == "ctrl+r"
}

func isCtrlEnter(msg tea.KeyMsg) bool {
	s := msg.String()
	return s == "ctrl+enter"
}

func parseOptions(raw string) ([]optionEntry, error) {
	var lastOpts []optionEntry
	search := raw
	for {
		idx := strings.Index(search, `{"options"`)
		if idx < 0 {
			break
		}
		segment := search[idx:]
		var resp optionResponse
		decoder := json.NewDecoder(strings.NewReader(segment))
		if err := decoder.Decode(&resp); err == nil && len(resp.Options) > 0 {
			opts := resp.Options
			sort.SliceStable(opts, func(i, j int) bool {
				oi := opts[i].RecommendationOrder
				oj := opts[j].RecommendationOrder
				if oi > 0 && oj > 0 && oi != oj {
					return oi < oj
				}
				if oi > 0 && oj <= 0 {
					return true
				}
				if oi <= 0 && oj > 0 {
					return false
				}
				return i < j
			})
			lastOpts = opts
		}
		// move past this occurrence
		search = search[idx+len(`{"options`):]
	}
	if len(lastOpts) > 0 {
		return lastOpts, nil
	}
	return nil, fmt.Errorf("failed to parse options JSON")
}

func (m *model) moveSelection(delta int) {
	if len(m.options) == 0 {
		return
	}
	m.selected = (m.selected + delta + len(m.options)) % len(m.options)
}

func (m model) selectedValue() string {
	if len(m.options) == 0 {
		return ""
	}
	if m.selected < 0 || m.selected >= len(m.options) {
		return ""
	}
	return m.options[m.selected].Value
}

func (m model) renderOptionsTable() string {
	if len(m.options) == 0 {
		noOptsStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)
		return noOptsStyle.Render("(no options)")
	}

	var rows []string

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Foreground(lipgloss.Color("230")).
		Bold(true).
		Padding(0, 1)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("15"))

	descStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245")).
		Italic(true)

	for i, opt := range m.options {
		value := cleanText(opt.Value)
		desc := cleanText(opt.Description)

		if i == m.selected {
			line := selectedStyle.Render("▶ " + value)
			rows = append(rows, line)
			if desc != "" {
				descLine := descStyle.Render("  " + desc)
				rows = append(rows, descLine)
			}
		} else {
			line := normalStyle.Render("  " + value)
			rows = append(rows, line)
			if desc != "" {
				descLine := descStyle.Render("  " + desc)
				rows = append(rows, descLine)
			}
		}

		if i < len(m.options)-1 {
			rows = append(rows, "")
		}
	}

	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2)

	return boxStyle.Render(strings.Join(rows, "\n"))
}

func cleanText(s string) string {
	s = strings.TrimSpace(s)
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", " ")), " ")
}

func buildPrompt(userPrompt string) string {
	base := "Give me one or more concise options with short descriptions for the following: "
	schema := `Respond ONLY with JSON shaped like {"options":[{"value":"...","description":"...","recommendation_order":1}]}. No extra text.`
	return base + userPrompt + "\n" + schema
}

func optionsSchemaPath() (string, error) {
	tryPaths := []string{}

	if exe, err := os.Executable(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(filepath.Dir(exe), "options.schema.json"))
	}
	if cwd, err := os.Getwd(); err == nil {
		tryPaths = append(tryPaths, filepath.Join(cwd, "options.schema.json"))
	}
	tryPaths = append(tryPaths, "/usr/local/share/insta-assist/options.schema.json")

	for _, p := range tryPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("options.schema.json not found in executable directory, working directory, or /usr/local/share/insta-assist")
}

func (m model) View() string {
	if !m.ready {
		loadingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
		return loadingStyle.Render("⏳ Loading...")
	}

	var b strings.Builder
	cli := m.currentCLI().name

	// Compact title line with logo and all available CLIs
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true).
		Underline(true)
	selectedCLIStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true)
	otherCLIStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Italic(true)
	shortcutStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true)

	// Logo emoji in fuscia/purple
	logoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205"))
	b.WriteString(logoStyle.Render("✨ "))

	b.WriteString(titleStyle.Render(titleText))
	b.WriteString(" • ")

	if m.running {
		runningStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)
		b.WriteString(runningStyle.Render(fmt.Sprintf("⚡ running %s…", cli)))
	} else {
		// Show all available CLIs with selected one highlighted
		for i, opt := range m.cliOptions {
			if i > 0 {
				b.WriteString(" ")
			}
			if i == m.cliIndex {
				b.WriteString(selectedCLIStyle.Render(opt.name))
			} else {
				b.WriteString(otherCLIStyle.Render(opt.name))
			}
		}
		// Show keyboard shortcuts
		if len(m.cliOptions) > 1 {
			b.WriteString(shortcutStyle.Render(" (ctrl+p/n)"))
		}
	}
	b.WriteString("\n")

	if m.mode == modeViewing {
		if strings.TrimSpace(m.lastPrompt) != "" {
			promptLabelStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("12")).
				Bold(true)
			promptStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("15"))

			b.WriteString("\n")
			b.WriteString(promptLabelStyle.Render("Prompt:"))
			b.WriteString("\n")
			b.WriteString(promptStyle.Render(m.lastPrompt))
			b.WriteString("\n")
		}
		b.WriteString("\n")

		if m.lastParseError != nil {
			errorStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Bold(true)
			b.WriteString(errorStyle.Render(fmt.Sprintf("❌ Could not parse options: %v\n", m.lastParseError)))
			if m.rawOutput != "" {
				rawStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245"))
				b.WriteString(rawStyle.Render("Raw output:\n"))
				b.WriteString(rawStyle.Render(m.rawOutput))
				b.WriteString("\n")
			}
		} else if len(m.options) == 0 {
			warnStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("11")).
				Bold(true)
			b.WriteString(warnStyle.Render("⚠ No options returned.\n"))
			if m.rawOutput != "" {
				rawStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245"))
				b.WriteString(rawStyle.Render("Raw output:\n"))
				b.WriteString(rawStyle.Render(m.rawOutput))
				b.WriteString("\n")
			}
		} else {
			optionsLabelStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("10")).
				Bold(true)
			b.WriteString(optionsLabelStyle.Render("✨ Options:"))
			b.WriteString("\n\n")
			b.WriteString(m.renderOptionsTable())
			b.WriteString("\n\n")

			selectedLabelStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("14")).
				Bold(true)
			selectedValueStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("11"))
			b.WriteString(selectedLabelStyle.Render("Selected: "))
			b.WriteString(selectedValueStyle.Render(m.selectedValue()))
			b.WriteString("\n")
		}
	} else {
		// Input mode with neon neo-tokyo gradient colors
		// Create neon gradient effect with multiple border colors
		inputBoxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{
				Light: "201",  // Bright magenta
				Dark:  "51",   // Bright cyan
			}).
			Padding(0, 1)

		// Calculate scroll indicator
		totalLines := strings.Count(m.input.Value(), "\n") + 1
		visibleHeight := m.input.Height()
		hasScroll := totalLines > visibleHeight

		// Build scroll indicator column with neon colors
		var scrollIndicator string
		if hasScroll {
			indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("201"))  // Bright magenta
			scrollLines := make([]string, visibleHeight+2) // +2 for top and bottom borders
			scrollLines[0] = "▲"
			scrollLines[len(scrollLines)-1] = "▼"
			for i := 1; i < len(scrollLines)-1; i++ {
				scrollLines[i] = "│"
			}
			scrollIndicator = indicatorStyle.Render(strings.Join(scrollLines, "\n"))
		}

		// Render the prompt box
		inputBox := inputBoxStyle.Render(m.input.View())

		// Build indent column (keep spacing where emoji was)
		inputLines := strings.Split(inputBox, "\n")
		emojiColumn := make([]string, len(inputLines))
		emojiColumn[0] = "  " // Two spaces to maintain indentation
		for i := 1; i < len(emojiColumn); i++ {
			emojiColumn[i] = "  " // Two spaces to align
		}
		emoji := strings.Join(emojiColumn, "\n")

		// Join horizontally: emoji + space + box + space + scroll indicator
		if hasScroll {
			combined := lipgloss.JoinHorizontal(lipgloss.Top, emoji, " ", inputBox, " ", scrollIndicator)
			b.WriteString(combined)
		} else {
			combined := lipgloss.JoinHorizontal(lipgloss.Top, emoji, " ", inputBox)
			b.WriteString(combined)
		}
		b.WriteString("\n")
	}

	if m.status != "" {
		statusStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Italic(true)
		b.WriteString(statusStyle.Render("💡 " + m.status))
	}

	return b.String()
}
