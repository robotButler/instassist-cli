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
	titleText      = "instassist"

	helpInput   = "tab: switch cli • enter: send • ctrl+r: send & auto-run • shift+enter/alt+enter: newline"
	helpViewing = "up/down/j/k: select • enter: copy & exit • ctrl+r: run & exit • shift+enter: new prompt • tab: switch cli • esc/q: quit"
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
		fmt.Printf("instassist version %s\n", version)
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
	default:
		log.Fatalf("unknown CLI: %s", cliName)
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

func newModel(defaultCLI string) model {
	schemaPath, err := optionsSchemaPath()
	if err != nil {
		log.Fatalf("schema not found: %v", err)
	}

	cliOptions := []cliOption{
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
	}

	input := textarea.New()
	input.Placeholder = "Enter prompt"
	input.Focus()
	input.CharLimit = 0
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.SetHeight(1) // Start with 1 line, will expand as needed

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
		return m, nil
	case responseMsg:
		return m.handleResponse(msg)
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	default:
	}

	if m.mode == modeInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
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
	if msg.String() == "tab" {
		m.toggleCLI()
		return m, nil
	}
	// Submit prompt on enter, add newline on shift+enter.
	if isShiftEnter(msg) {
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
	if isCtrlR(msg) {
		// Submit and auto-execute first result
		m.autoExecute = true
		return m.submitPrompt()
	}
	if msg.Type == tea.KeyEnter {
		// Regular submit - no auto-execute
		m.autoExecute = false
		return m.submitPrompt()
	}

	return m.updateInput(msg)
}

func (m model) handleViewingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC || msg.String() == "esc" || msg.String() == "q":
		return m, tea.Quit
	case msg.String() == "tab":
		m.toggleCLI()
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
	if msg.String() == "tab" {
		m.toggleCLI()
		return m, nil
	}
	return m, nil
}

func (m *model) adjustTextareaHeight() {
	// Dynamically adjust height based on content (1-10 lines)
	lines := strings.Count(m.input.Value(), "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 10 {
		lines = 10
	}
	m.input.SetHeight(lines)
}

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
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

func (m model) toggleCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex + 1) % len(m.cliOptions)
	if m.mode == modeInput {
		m.status = helpInput
	} else if m.mode == modeViewing {
		m.status = helpViewing
	}
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
	if msg.Type == tea.KeyCtrlJ {
		return true
	}
	if msg.Type == tea.KeyEnter && msg.Alt {
		return true
	}
	s := msg.String()
	return s == "shift+enter" || s == "alt+enter"
}

func isCtrlR(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlR || msg.String() == "ctrl+r"
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
	tryPaths = append(tryPaths, "/usr/local/share/instassist/options.schema.json")

	for _, p := range tryPaths {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("options.schema.json not found in executable directory, working directory, or /usr/local/share/instassist")
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

	// Compact title line: "instassist • CLI: codex (tab to switch)" or "instassist • ⚡ running codex…"
	titleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("205")).
		Bold(true).
		Underline(true)
	cliStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("14")).
		Bold(true)
	cliLabelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241"))

	b.WriteString(titleStyle.Render(titleText))
	b.WriteString(" • ")

	if m.running {
		runningStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)
		b.WriteString(runningStyle.Render(fmt.Sprintf("⚡ running %s…", cli)))
	} else {
		b.WriteString(cliLabelStyle.Render("CLI: "))
		b.WriteString(cliStyle.Render(cli))
		b.WriteString(cliLabelStyle.Render(" (tab to switch)"))
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
			b.WriteString(promptLabelStyle.Render("📝 Prompt:"))
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
		// Input mode: emoji on same line as prompt box
		promptLabelStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

		inputBoxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(0, 1)

		// Calculate scroll indicator
		totalLines := strings.Count(m.input.Value(), "\n") + 1
		visibleHeight := m.input.Height()
		hasScroll := totalLines > visibleHeight

		// Build scroll indicator column
		var scrollIndicator string
		if hasScroll {
			indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
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

		// Build emoji column (aligned to top)
		inputLines := strings.Split(inputBox, "\n")
		emojiColumn := make([]string, len(inputLines))
		emojiColumn[0] = promptLabelStyle.Render("📝")
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
