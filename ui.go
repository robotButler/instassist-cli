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
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

const (
	titleText = "insta-assist"

	helpInput   = "enter: send • ctrl+r: send & run • ctrl+y: toggle yolo • alt+enter/ctrl+j: newline • esc: exit"
	helpViewing = "enter: copy & exit • ctrl+r: run & exit • a: refine • n: new prompt • ctrl+y: toggle yolo • esc/q: quit"
	helpRefine  = "enter: refine • ctrl+r: refine & run • ctrl+y: toggle yolo • alt+enter/ctrl+j: newline • esc: exit"
)

type viewMode int

const (
	modeInput viewMode = iota
	modeRunning
	modeViewing
	modeRefine
)

type responseMsg struct {
	output []byte
	err    error
	cli    string
}

type execResultMsg struct {
	err    error
	exit   bool
	output string
}

type tickMsg struct{}

func tickCmd() tea.Msg {
	time.Sleep(80 * time.Millisecond)
	return tickMsg{}
}

type clickRegion struct {
	kind   string
	index  int
	startX int
	endX   int
	y      int
}

type headerMeta struct {
	cliRegions  []clickRegion
	yoloRegion  clickRegion
	headerWidth int
}

type cliOption struct {
	name         string
	runPrompt    func(ctx context.Context, prompt string, yolo bool) ([]byte, error)
	resumePrompt func(ctx context.Context, prompt string, sessionID string, yolo bool) ([]byte, error)
}

type model struct {
	cliOptions []cliOption
	cliIndex   int

	input textarea.Model

	mode         viewMode
	running      bool
	stayOpenExec bool
	yolo         bool

	width  int
	height int
	ready  bool

	lastPrompt string
	status     string

	rawOutput  string
	execOutput string

	options        []optionEntry
	selected       int
	lastParseError error
	lastError      error

	autoExecute bool // if true, execute first result and exit

	spinnerFrame int // for animation while waiting

	sessionIDs      map[string]string
	pendingResumeID string
	promptHistory   []string
}

func newModel(defaultCLI string, stayOpenExec bool) model {
	schemaPath, schemaJSON, err := schemaSources()
	if err != nil {
		logFatalSchema(err)
	}

	allCLIOptions := []cliOption{
		{
			name: "codex",
			runPrompt: func(ctx context.Context, prompt string, yolo bool) ([]byte, error) {
				args := []string{"exec", "--output-schema", schemaPath, "--skip-git-repo-check", "--json"}
				if yolo {
					args = append(args, "--yolo")
				}
				cmd := exec.CommandContext(ctx, "codex", args...)
				cmd.Stdin = strings.NewReader(prompt)
				return cmd.CombinedOutput()
			},
			resumePrompt: func(ctx context.Context, prompt string, sessionID string, yolo bool) ([]byte, error) {
				args := []string{"exec", "resume"}
				if yolo {
					args = append(args, "--yolo")
				}
				args = append(args, "--output-schema", schemaPath, "--skip-git-repo-check", "--json", sessionID, "-")
				cmd := exec.CommandContext(ctx, "codex", args...)
				cmd.Stdin = strings.NewReader(prompt)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "claude",
			runPrompt: func(ctx context.Context, prompt string, yolo bool) ([]byte, error) {
				args := []string{"-p", prompt, "--print", "--output-format", "json", "--json-schema", schemaJSON}
				if yolo {
					args = append(args, "--dangerously-skip-permissions")
				}
				cmd := exec.CommandContext(ctx, "claude", args...)
				return cmd.CombinedOutput()
			},
			resumePrompt: func(ctx context.Context, prompt string, sessionID string, yolo bool) ([]byte, error) {
				args := []string{"-p", prompt, "--print", "--output-format", "json", "--json-schema", schemaJSON, "--resume", sessionID}
				if yolo {
					args = append(args, "--dangerously-skip-permissions")
				}
				cmd := exec.CommandContext(ctx, "claude", args...)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "gemini",
			runPrompt: func(ctx context.Context, prompt string, yolo bool) ([]byte, error) {
				args := []string{"--output-format", "json"}
				if yolo {
					args = append(args, "--yolo")
				}
				args = append(args, prompt)
				cmd := exec.CommandContext(ctx, "gemini", args...)
				return cmd.CombinedOutput()
			},
			resumePrompt: func(ctx context.Context, prompt string, sessionID string, yolo bool) ([]byte, error) {
				args := []string{"--output-format", "json", "--resume", sessionID}
				if yolo {
					args = append(args, "--yolo")
				}
				args = append(args, prompt)
				cmd := exec.CommandContext(ctx, "gemini", args...)
				return cmd.CombinedOutput()
			},
		},
		{
			name: "opencode",
			runPrompt: func(ctx context.Context, prompt string, yolo bool) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", prompt)
				return cmd.CombinedOutput()
			},
			resumePrompt: func(ctx context.Context, prompt string, sessionID string, yolo bool) ([]byte, error) {
				cmd := exec.CommandContext(ctx, "opencode", "run", "--format", "json", "--session", sessionID, prompt)
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
		cliOptions:   cliOptions,
		cliIndex:     cliIndex,
		input:        input,
		mode:         modeInput,
		status:       helpInput,
		stayOpenExec: stayOpenExec,
		sessionIDs:   map[string]string{},
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
		m.adjustTextareaHeight()
		return m, nil
	case tickMsg:
		if m.running {
			m.spinnerFrame = (m.spinnerFrame + 1) % 10
			return m, tickCmd
		}
		return m, nil
	case responseMsg:
		return m.handleResponse(msg)
	case execResultMsg:
		if msg.err != nil {
			m.running = false
			m.mode = modeViewing
			m.status = fmt.Sprintf("❌ exec failed: %v • %s", msg.err, helpViewing)
			m.lastError = msg.err
			m.execOutput = msg.output
			return m, nil
		}
		if msg.exit {
			return m, tea.Quit
		}
		m.running = false
		m.mode = modeViewing
		m.execOutput = msg.output
		m.status = "command finished • " + helpViewing
		return m, nil
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.MouseMsg:
		return m.handleMouseMsg(msg)
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
	m.lastError = nil
	m.execOutput = ""

	if sessionID := extractSessionID(respText); sessionID != "" {
		if m.sessionIDs == nil {
			m.sessionIDs = map[string]string{}
		}
		m.sessionIDs[msg.cli] = sessionID
	}

	if msg.err != nil {
		m.lastError = msg.err
		m.status = fmt.Sprintf("error from %s: %v • %s", msg.cli, msg.err, helpViewing)
		m.options = nil
		m.selected = 0
		return m, nil
	}

	opts, parseErr := extractOptions(respText)
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
		return m, execWithFeedback(value, !m.stayOpenExec, m.stayOpenExec)
	}

	return m, nil
}

func (m model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeInput:
		return m.handleInputKeys(msg)
	case modeRefine:
		return m.handleInputKeys(msg)
	case modeRunning:
		return m.handleRunningKeys(msg)
	case modeViewing:
		return m.handleViewingKeys(msg)
	default:
		return m, nil
	}
}

func (m model) handleMouseMsg(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseButtonLeft || msg.Action != tea.MouseActionPress {
		return m, nil
	}

	if msg.Y == 0 {
		layout := m.headerLayout()
		currentHelp := helpInput
		if m.mode == modeViewing {
			currentHelp = helpViewing
		} else if m.mode == modeRefine {
			currentHelp = helpRefine
		}
		for _, reg := range layout.cliRegions {
			if msg.X >= reg.startX && msg.X < reg.endX {
				m.cliIndex = reg.index
				m.status = currentHelp
				return m, nil
			}
		}
		if msg.X >= layout.yoloRegion.startX && msg.X < layout.yoloRegion.endX {
			m.toggleYolo()
			return m, nil
		}
	}

	if m.mode == modeViewing || m.mode == modeRefine {
		if idx := m.optionIndexAt(msg.Y); idx >= 0 {
			m.selected = idx
			return m, nil
		}
	}

	return m, nil
}

func (m model) handleInputKeys(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	if msg.Type == tea.KeyCtrlY || msg.String() == "ctrl+y" {
		m.toggleYolo()
		return m, nil
	}
	// ctrl-p = previous (left), ctrl-n = next (right)
	if msg.Type == tea.KeyCtrlP {
		m.prevCLI()
		return m, nil
	}
	if msg.Type == tea.KeyCtrlN {
		m.nextCLI()
		return m, nil
	}
	// Handle tab key - insert tab character
	if msg.Type == tea.KeyTab {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'\t'}})
		return m, cmd
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
	case msg.Type == tea.KeyCtrlY || msg.String() == "ctrl+y":
		m.toggleYolo()
		return m, nil
	case msg.String() == "a":
		sessionID := m.sessionIDs[m.currentCLI().name]
		if sessionID == "" {
			m.status = "no session to refine yet • " + helpViewing
			return m, nil
		}
		m.mode = modeRefine
		m.running = false
		m.input.SetValue("")
		m.input.Focus()
		m.status = helpRefine
		m.selected = -1
		m.autoExecute = false
		m.pendingResumeID = sessionID
		m.adjustTextareaHeight()
		return m, nil
	case msg.String() == "n":
		m.mode = modeInput
		m.running = false
		m.input.SetValue("")
		m.input.Focus()
		m.status = helpInput
		m.options = nil
		m.lastParseError = nil
		m.rawOutput = ""
		m.lastPrompt = ""
		m.autoExecute = false
		m.execOutput = ""
		m.selected = 0
		m.pendingResumeID = ""
		m.promptHistory = nil
		m.lastError = nil
		m.adjustTextareaHeight()
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
		m.execOutput = ""
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
		m.execOutput = ""
		return m, execWithFeedback(value, !m.stayOpenExec, m.stayOpenExec)
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
	// Only allow quitting while running
	if msg.Type == tea.KeyCtrlC || msg.String() == "esc" {
		return m, tea.Quit
	}
	return m, nil
}

func (m *model) toggleYolo() {
	m.yolo = !m.yolo
	state := "off"
	if m.yolo {
		state = "on"
	}
	switch m.mode {
	case modeRefine:
		m.status = fmt.Sprintf("yolo: %s • %s", state, helpRefine)
	case modeViewing:
		m.status = fmt.Sprintf("yolo: %s • %s", state, helpViewing)
	default:
		m.status = fmt.Sprintf("yolo: %s • %s", state, helpInput)
	}
}

func (m *model) adjustTextareaHeight() {
	content := m.input.Value()
	visibleLines := strings.Count(content, "\n") + 1

	// Account for wrapped lines based on display width (runewidth-aware).
	if m.input.Width() > 0 {
		for _, ln := range strings.Split(content, "\n") {
			w := runewidth.StringWidth(ln)
			if w > m.input.Width() {
				extra := (w + m.input.Width() - 1) / m.input.Width()
				// extra already includes the first line; subtract 1 to add only wraps.
				visibleLines += extra - 1
			}
		}
	}

	if visibleLines < 1 {
		visibleLines = 1
	}
	if visibleLines > 20 {
		visibleLines = 20
	}

	if m.input.Height() != visibleLines {
		m.input.SetHeight(visibleLines)
		// Re-set the value to force viewport recalculation and keep cursor at end.
		val := m.input.Value()
		m.input.SetValue(val)
	}
}

func (m model) optionIndexAt(y int) int {
	row := 1 // header occupies row 0
	if len(m.promptHistory) > 0 {
		row += len(m.promptHistory)
	} else if strings.TrimSpace(m.lastPrompt) != "" {
		row++
	}

	if m.lastError != nil || m.lastParseError != nil || len(m.options) == 0 {
		return -1
	}

	if y >= row && y < row+len(m.options) {
		return y - row
	}

	return -1
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
		if m.mode == modeRefine {
			m.status = "prompt is empty • " + helpRefine
		} else {
			m.status = "prompt is empty • " + helpInput
		}
		return m, nil
	}

	wasRefine := m.mode == modeRefine
	if wasRefine && len(m.promptHistory) > 0 {
		m.promptHistory = append(m.promptHistory, userPrompt)
	} else {
		m.promptHistory = []string{userPrompt}
	}

	m.lastPrompt = userPrompt
	combinedPrompt := strings.Join(m.promptHistory, "\n")
	fullPrompt := buildPrompt(combinedPrompt)
	m.running = true
	m.mode = modeRunning
	m.spinnerFrame = 0
	m.status = ""
	if !wasRefine {
		m.options = nil
	}
	m.lastParseError = nil
	m.rawOutput = ""
	m.execOutput = ""
	m.selected = 0
	sessionID := ""
	if wasRefine {
		sessionID = m.pendingResumeID
	}
	m.pendingResumeID = ""

	selectedCLI := m.currentCLI()
	cliName := selectedCLI.name
	cmd := func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		runPrompt := selectedCLI.runPrompt
		if sessionID != "" && selectedCLI.resumePrompt != nil {
			runPrompt = func(ctx context.Context, prompt string, yolo bool) ([]byte, error) {
				return selectedCLI.resumePrompt(ctx, prompt, sessionID, yolo)
			}
		}
		out, err := runPrompt(ctx, fullPrompt, m.yolo)
		return responseMsg{
			output: out,
			err:    err,
			cli:    cliName,
		}
	}

	m.resizeComponents()
	return m, tea.Batch(cmd, tickCmd)
}

func (m *model) nextCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex + 1) % len(m.cliOptions)
	m.status = helpInput
}

func (m *model) prevCLI() {
	if len(m.cliOptions) == 0 {
		return
	}
	m.cliIndex = (m.cliIndex - 1 + len(m.cliOptions)) % len(m.cliOptions)
	m.status = helpInput
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
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("15"))

	commentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("245"))

	for i, opt := range m.options {
		value := cleanText(opt.Value)
		desc := cleanText(opt.Description)

		var line string
		if i == m.selected {
			if desc != "" {
				line = selectedStyle.Render("▶ "+value) + "  " + commentStyle.Render("# "+desc)
			} else {
				line = selectedStyle.Render("▶ " + value)
			}
		} else {
			if desc != "" {
				line = normalStyle.Render("  "+value) + "  " + commentStyle.Render("# "+desc)
			} else {
				line = normalStyle.Render("  " + value)
			}
		}
		rows = append(rows, line)
	}

	return strings.Join(rows, "\n")
}

func (m model) renderPromptHistory() string {
	promptStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("12")).
		Bold(true)

	if len(m.promptHistory) == 0 && strings.TrimSpace(m.lastPrompt) == "" {
		return ""
	}

	var sb strings.Builder
	if len(m.promptHistory) > 0 {
		sb.WriteString(promptStyle.Render("❯ " + m.promptHistory[0]))
		sb.WriteString("\n")
		for _, p := range m.promptHistory[1:] {
			sb.WriteString(promptStyle.Render("↳ " + p))
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(promptStyle.Render("❯ " + m.lastPrompt))
		sb.WriteString("\n")
	}

	return sb.String()
}

func (m model) renderInputArea() string {
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
		return combined + "\n"
	}

	combined := lipgloss.JoinHorizontal(lipgloss.Top, emoji, " ", inputBox)
	return combined + "\n"
}

func (m model) buildHeader() (string, headerMeta) {
	var meta headerMeta

	logoStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	separatorStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("205")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	selectedCLIStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("205")).
		Foreground(lipgloss.Color("0")).
		Bold(true).
		Padding(0, 1)

	normalCLIStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Padding(0, 1)

	toggleStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("205")).
		Padding(0, 1)

	targetWidth := m.width - 10
	if targetWidth < 20 {
		targetWidth = 20
	}

	var leftSide strings.Builder
	cursor := 0

	logo := logoStyle.Render("✨ ")
	leftSide.WriteString(logo)
	cursor += lipgloss.Width(logo)

	title := titleStyle.Render(titleText)
	leftSide.WriteString(title)
	cursor += lipgloss.Width(title)

	sep := separatorStyle.Render(" • ")
	leftSide.WriteString(sep)
	cursor += lipgloss.Width(sep)

	for i, opt := range m.cliOptions {
		if i > 0 {
			p := separatorStyle.Render(" | ")
			leftSide.WriteString(p)
			cursor += lipgloss.Width(p)
		}
		tab := normalCLIStyle.Render(opt.name)
		if i == m.cliIndex {
			tab = selectedCLIStyle.Render(opt.name)
		}
		start := cursor
		cursor += lipgloss.Width(tab)
		leftSide.WriteString(tab)
		meta.cliRegions = append(meta.cliRegions, clickRegion{
			kind:   "cli",
			index:  i,
			startX: start,
			endX:   cursor,
			y:      0,
		})
	}

	space := separatorStyle.Render(" ")
	leftSide.WriteString(space)
	cursor += lipgloss.Width(space)

	ctrlHint := keyStyle.Render("ctrl+n/p")
	leftSide.WriteString(ctrlHint)
	cursor += lipgloss.Width(ctrlHint)

	leftWidth := lipgloss.Width(leftSide.String())

	yoloState := "off"
	if m.yolo {
		yoloState = "on"
	}

	toggleText := toggleStyle.Render("yolo: " + yoloState)
	rightSide := keyStyle.Render("ctrl+y") + descStyle.Render(" ") + toggleText
	rightWidth := lipgloss.Width(rightSide)

	spacing := ""
	if targetWidth > leftWidth+rightWidth+2 {
		spacing = strings.Repeat(" ", targetWidth-leftWidth-rightWidth)
	} else {
		spacing = "  "
	}

	header := leftSide.String() + spacing + rightSide

	meta.yoloRegion = clickRegion{
		kind:   "yolo",
		startX: lipgloss.Width(leftSide.String()) + lipgloss.Width(spacing) + lipgloss.Width(keyStyle.Render("ctrl+y")+descStyle.Render(" ")),
		endX:   lipgloss.Width(header),
		y:      0,
	}
	meta.headerWidth = lipgloss.Width(header)

	return header, meta
}

func (m model) headerLayout() headerMeta {
	_, meta := m.buildHeader()
	return meta
}

func (m model) View() string {
	if !m.ready {
		loadingStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
		return loadingStyle.Render("⏳ Loading...")
	}

	var b strings.Builder

	header, _ := m.buildHeader()
	b.WriteString(header)
	b.WriteString("\n")

	if m.running {
		// Show spinner animation
		spinnerFrames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		spinner := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]

		spinnerStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)
		b.WriteString(spinnerStyle.Render(fmt.Sprintf("%s Running %s...", spinner, m.currentCLI().name)))
		b.WriteString("\n")
		if ph := strings.TrimSuffix(m.renderPromptHistory(), "\n"); ph != "" {
			b.WriteString(ph)
			b.WriteString("\n")
		}
		if len(m.options) > 0 {
			b.WriteString(m.renderOptionsTable())
			b.WriteString("\n")
		}
	} else if m.mode == modeViewing || m.mode == modeRefine {
		if ph := strings.TrimSuffix(m.renderPromptHistory(), "\n"); ph != "" {
			b.WriteString(ph)
			b.WriteString("\n")
		}

		if m.lastError != nil {
			errorStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Bold(true)
			b.WriteString(errorStyle.Render(fmt.Sprintf("❌ Error: %v", m.lastError)))
			b.WriteString("\n")
			if m.rawOutput != "" {
				rawStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245"))
				b.WriteString(rawStyle.Render(m.rawOutput))
				b.WriteString("\n")
			}
		} else if m.lastParseError != nil {
			errorStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("9")).
				Bold(true)
			b.WriteString(errorStyle.Render(fmt.Sprintf("❌ Parse error: %v", m.lastParseError)))
			b.WriteString("\n")
			if m.rawOutput != "" {
				rawStyle := lipgloss.NewStyle().
					Foreground(lipgloss.Color("245"))
				b.WriteString(rawStyle.Render(m.rawOutput))
				b.WriteString("\n")
			}
		} else if len(m.options) == 0 {
			warnStyle := lipgloss.NewStyle().
				Foreground(lipgloss.Color("11")).
				Bold(true)
			b.WriteString(warnStyle.Render("⚠ No options returned"))
			b.WriteString("\n")
		} else {
			b.WriteString(m.renderOptionsTable())
			b.WriteString("\n")
			// Add horizontal divider before status line
			dividerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
			dividerWidth := m.width - 10
			if dividerWidth < 20 {
				dividerWidth = 20
			}
			b.WriteString(dividerStyle.Render(strings.Repeat("─", dividerWidth)))
			b.WriteString("\n")
		}

		if strings.TrimSpace(m.execOutput) != "" {
			outputLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true)
			outputText := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
			b.WriteString(outputLabel.Render("Command output:"))
			b.WriteString("\n")
			b.WriteString(outputText.Render(m.execOutput))
			b.WriteString("\n")
		}

		if m.mode == modeRefine {
			b.WriteString(m.renderInputArea())
		}
	} else {
		b.WriteString(m.renderInputArea())
	}

	if m.status != "" {
		// Style keyboard shortcuts differently from descriptions
		keyStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("205")).
			Bold(true)
		sepStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
		descStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

		b.WriteString(descStyle.Render("💡 "))

		// Build styled help text based on current status
		if m.status == helpInput {
			b.WriteString(keyStyle.Render("enter"))
			b.WriteString(descStyle.Render(": send "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+r"))
			b.WriteString(descStyle.Render(": send & run "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+y"))
			b.WriteString(descStyle.Render(": toggle yolo "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("alt+enter"))
			b.WriteString(descStyle.Render("/"))
			b.WriteString(keyStyle.Render("ctrl+j"))
			b.WriteString(descStyle.Render(": newline "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("esc"))
			b.WriteString(descStyle.Render(": exit"))
		} else if m.status == helpViewing {
			b.WriteString(keyStyle.Render("enter"))
			b.WriteString(descStyle.Render(": copy & exit "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+r"))
			b.WriteString(descStyle.Render(": run & exit "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("a"))
			b.WriteString(descStyle.Render(": refine "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("n"))
			b.WriteString(descStyle.Render(": new prompt "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+y"))
			b.WriteString(descStyle.Render(": toggle yolo "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("esc"))
			b.WriteString(descStyle.Render("/"))
			b.WriteString(keyStyle.Render("q"))
			b.WriteString(descStyle.Render(": quit"))
		} else if m.status == helpRefine {
			b.WriteString(keyStyle.Render("enter"))
			b.WriteString(descStyle.Render(": refine "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+r"))
			b.WriteString(descStyle.Render(": refine & run "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("ctrl+y"))
			b.WriteString(descStyle.Render(": toggle yolo "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("alt+enter"))
			b.WriteString(descStyle.Render("/"))
			b.WriteString(keyStyle.Render("ctrl+j"))
			b.WriteString(descStyle.Render(": newline "))
			b.WriteString(sepStyle.Render("• "))
			b.WriteString(keyStyle.Render("esc"))
			b.WriteString(descStyle.Render(": exit"))
		} else {
			// For other status messages, just render as-is
			b.WriteString(descStyle.Render(m.status))
		}
	}

	return b.String()
}

func execWithFeedback(value string, exitOnSuccess bool, stayOpenExec bool) tea.Cmd {
	if stayOpenExec {
		return func() tea.Msg {
			cmd := exec.Command("sh", "-c", value)
			out, err := cmd.CombinedOutput()
			return execResultMsg{err: err, exit: false, output: string(out)}
		}
	}

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
