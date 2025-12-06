package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os/exec"
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
	defaultCLIName = "codex"
	titleText      = "instassist"

	helpInput   = "tab: switch cli • enter: send • shift+enter/alt+enter: newline"
	helpViewing = "up/down/j/k: select • enter: copy & exit • shift+enter/alt+enter: new prompt • tab: switch cli • esc/q: quit"
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
}

func main() {
	cliFlag := flag.String("cli", defaultCLIName, "default CLI to use: codex or claude")
	flag.Parse()

	m := newModel(*cliFlag)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func newModel(defaultCLI string) model {
	cliOptions := []cliOption{
		{
			name: "codex",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "codex", "exec")
				cmd.Stdin = strings.NewReader(prompt)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "claude",
			runPrompt: func(ctx context.Context, prompt string) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "claude", "-p", prompt)
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
	input.SetHeight(5)

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
	if msg.String() == "tab" {
		m.toggleCLI()
		return m, nil
	}
	// Submit prompt on enter, add newline on shift+enter.
	if isShiftEnter(msg) {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return m, cmd
	}
	if msg.Type == tea.KeyEnter {
		return m.submitPrompt()
	}

	return m.updateInput(msg)
}

func (m model) handleViewingKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "esc" || msg.String() == "q":
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
		return m, nil
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
			m.status = fmt.Sprintf("copy failed: %v • %s", err, helpViewing)
			return m, nil
		}
		return m, tea.Quit
	case msg.String() == "up" || msg.String() == "k":
		m.moveSelection(-1)
	case msg.String() == "down" || msg.String() == "j":
		m.moveSelection(1)
	}
	return m, nil
}

func (m model) handleRunningKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "tab" {
		m.toggleCLI()
		return m, nil
	}
	return m, nil
}

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
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

	if m.width > 4 {
		m.input.SetWidth(m.width - 2)
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

func parseOptions(raw string) ([]optionEntry, error) {
	tryParse := func(s string) ([]optionEntry, error) {
		var resp optionResponse
		if err := json.Unmarshal([]byte(s), &resp); err != nil {
			return nil, err
		}
		if len(resp.Options) == 0 {
			return nil, fmt.Errorf("no options returned")
		}
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
		return opts, nil
	}

	if opts, err := tryParse(raw); err == nil {
		return opts, nil
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		if opts, err := tryParse(raw[start : end+1]); err == nil {
			return opts, nil
		}
	}
	return nil, fmt.Errorf("failed to parse options from CLI output")
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
		return "(no options)"
	}

	var rows []string
	header := fmt.Sprintf("%-6s │ %-s", "Order", "Option")
	rows = append(rows, header)

	rowStyle := lipgloss.NewStyle()
	selStyle := lipgloss.NewStyle().Reverse(true)

	for i, opt := range m.options {
		order := opt.RecommendationOrder
		if order <= 0 {
			order = i + 1
		}
		line := fmt.Sprintf("%-6d │ %s — %s", order, cleanText(opt.Value), cleanText(opt.Description))
		if i == m.selected {
			line = selStyle.Render(line)
		} else {
			line = rowStyle.Render(line)
		}
		rows = append(rows, line)
	}

	return strings.Join(rows, "\n")
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

func (m model) View() string {
	if !m.ready {
		return "loading..."
	}

	var b strings.Builder
	cli := m.currentCLI().name
	title := lipgloss.NewStyle().Bold(true).Render(titleText)
	b.WriteString(title)
	if m.running {
		b.WriteString("  ")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(fmt.Sprintf("running %s…", cli)))
	}
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("CLI: %s (tab to switch)\n", cli))

	if m.mode == modeViewing {
		if strings.TrimSpace(m.lastPrompt) != "" {
			b.WriteString("\n")
			b.WriteString("Prompt:\n")
			b.WriteString(m.lastPrompt)
			b.WriteString("\n")
		}
		b.WriteString("\n")
		if m.lastParseError != nil {
			b.WriteString(fmt.Sprintf("Could not parse options: %v\n", m.lastParseError))
			if m.rawOutput != "" {
				b.WriteString("Raw output:\n")
				b.WriteString(m.rawOutput)
				b.WriteString("\n")
			}
		} else if len(m.options) == 0 {
			b.WriteString("No options returned.\n")
			if m.rawOutput != "" {
				b.WriteString("Raw output:\n")
				b.WriteString(m.rawOutput)
				b.WriteString("\n")
			}
		} else {
			b.WriteString(m.renderOptionsTable())
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n")
		b.WriteString("Prompt:\n")
		b.WriteString(m.input.View())
		b.WriteString("\n")
	}

	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(m.status)
	}

	return b.String()
}
