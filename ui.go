package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	titleText = "insta-assist"

	helpInput   = "enter: send • ctrl+r: send & run • ctrl+n/ctrl+p: switch cli • alt+enter/ctrl+j: newline"
	helpViewing = "up/down/j/k: select • enter: copy & exit • ctrl+r: run & exit • ctrl+n/ctrl+p: switch cli • alt+enter: new prompt • esc/q: quit"
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

type execResultMsg struct {
	err  error
	exit bool
}

type cliOption struct {
	name      string
	runPrompt func(ctx context.Context, prompt string) ([]byte, error)
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

func newModel(defaultCLI string) model {
	schemaPath, err := optionsSchemaPath()
	if err != nil {
		logFatalSchema(err)
	}

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
				cmd := exec.CommandContext(ctx, "gemini", "--output-format", "json", prompt)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "opencode",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", prompt)
				return cmd.CombinedOutput()
			},
		},
	}

	var cliOptions []cliOption
	for _, opt := range allCLIOptions {
		if cliAvailable(opt.name) {
			cliOptions = append(cliOptions, opt)
		}
	}

	if len(cliOptions) == 0 {
		logFatalSchema(fmt.Errorf("no AI CLIs found. Please install at least one of: codex, claude, gemini, opencode"))
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
		if m.mode == modeInput {
			m.adjustTextareaHeight()
		}
		return m, nil
	case responseMsg:
		return m.handleResponse(msg)
	case execResultMsg:
		if msg.err != nil {
			m.running = false
			m.mode = modeViewing
			m.status = fmt.Sprintf("❌ exec failed: %v • %s", msg.err, helpViewing)
			return m, nil
		}
		if msg.exit {
			return m, tea.Quit
		}
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	if m.mode == modeInput {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
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

	if m.autoExecute && len(opts) > 0 {
		value := opts[0].Value
		m.status = fmt.Sprintf("running: %s", cleanText(value))
		m.autoExecute = false
		return m, execWithFeedback(value, true)
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
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	if msg.String() == "ctrl+n" {
		m.nextCLI()
		return m, nil
	}
	if msg.String() == "ctrl+p" {
		m.prevCLI()
		return m, nil
	}
	if isNewline(msg) {
		currentLines := strings.Count(m.input.Value(), "\n") + 1
		newLines := currentLines + 1
		if newLines <= 10 {
			m.input.SetHeight(newLines)
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return m, cmd
	}
	if isCtrlR(msg) {
		m.autoExecute = true
		return m.submitPrompt()
	}
	if msg.Type == tea.KeyEnter || isCtrlEnter(msg) {
		m.autoExecute = false
		return m.submitPrompt()
	}
	return m.updateInput(msg)
}

func (m model) handleViewingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.Type == tea.KeyCtrlC || msg.String() == "esc" || msg.String() == "q":
		return m, tea.Quit
	case msg.String() == "ctrl+n":
		m.nextCLI()
		return m, nil
	case msg.String() == "ctrl+p":
		m.prevCLI()
		return m, nil
	case isNewline(msg):
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
		value := m.selectedValue()
		if value == "" {
			if m.rawOutput == "" {
				m.status = "nothing to run • " + helpViewing
				return m, nil
			}
			value = m.rawOutput
		}
		m.status = fmt.Sprintf("running: %s", cleanText(value))
		return m, execWithFeedback(value, true)
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
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	if msg.String() == "ctrl+n" {
		m.nextCLI()
		return m, nil
	}
	if msg.String() == "ctrl+p" {
		m.prevCLI()
		return m, nil
	}
	return m, nil
}

func (m *model) adjustTextareaHeight() {
	content := m.input.Value()
	lines := strings.Count(content, "\n") + 1
	if lines < 1 {
		lines = 1
	}
	if lines > 1 {
		lines++
	}
	if lines > 20 {
		lines = 20
	}

	m.input.SetHeight(lines)
}

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		if keyMsg.Type == tea.KeyEnter {
			content := m.input.Value()
			futureLines := strings.Count(content, "\n") + 2
			if futureLines > 1 {
				futureLines++
			}
			if futureLines > 20 {
				futureLines = 20
			}
			m.input.SetHeight(futureLines)
		}
	}

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
	m.status = fmt.Sprintf("running %s… • ctrl+n/ctrl+p: switch cli", m.currentCLI().name)
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
	m.status = fmt.Sprintf("using %s • %s", m.currentCLI().name, helpInput)
}

func (m *model) prevCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex - 1 + len(m.cliOptions)) % len(m.cliOptions)
	m.status = fmt.Sprintf("using %s • %s", m.currentCLI().name, helpInput)
}

func (m model) currentCLI() cliOption {
	return m.cliOptions[m.cliIndex]
}

func (m *model) resizeComponents() {
	if !m.ready {
		return
	}

	if m.width > 10 {
		m.input.SetWidth(m.width - 10)
	}
}

func isNewline(msg tea.KeyMsg) bool {
	if msg.Type == tea.KeyCtrlJ {
		return true
	}
	if msg.Type == tea.KeyEnter && msg.Alt {
		return true
	}
	return msg.String() == "alt+enter"
}

func isCtrlR(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlR || msg.String() == "ctrl+r"
}

func isCtrlEnter(msg tea.KeyMsg) bool {
	return msg.String() == "ctrl+enter"
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

func (m model) View() string {
	if !m.ready {
		loadingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
		return loadingStyle.Render("⏳ Loading...")
	}

	var b strings.Builder
	cli := m.currentCLI().name

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
		if len(m.cliOptions) > 1 {
			b.WriteString(shortcutStyle.Render(" (ctrl+n / ctrl+p)"))
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
		inputBoxStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.AdaptiveColor{
				Light: "201",
				Dark:  "51",
			}).
			Padding(0, 1)

		totalLines := strings.Count(m.input.Value(), "\n") + 1
		visibleHeight := m.input.Height()
		hasScroll := totalLines > visibleHeight

		var scrollIndicator string
		if hasScroll {
			indicatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("201"))
			scrollLines := make([]string, visibleHeight+2)
			scrollLines[0] = "▲"
			scrollLines[len(scrollLines)-1] = "▼"
			for i := 1; i < len(scrollLines)-1; i++ {
				scrollLines[i] = "│"
			}
			scrollIndicator = indicatorStyle.Render(strings.Join(scrollLines, "\n"))
		}

		inputBox := inputBoxStyle.Render(m.input.View())

		inputLines := strings.Split(inputBox, "\n")
		emojiColumn := make([]string, len(inputLines))
		emojiColumn[0] = "  "
		for i := 1; i < len(emojiColumn); i++ {
			emojiColumn[i] = "  "
		}
		emoji := strings.Join(emojiColumn, "\n")

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

func execWithFeedback(value string, exitOnSuccess bool) tea.Cmd {
	cmd := exec.Command("sh", "-c", value)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return execResultMsg{err: err, exit: false}
		}
		return execResultMsg{exit: exitOnSuccess}
	})
}

func logFatalSchema(err error) {
	log.Fatalf("schema not found: %v", err)
}
