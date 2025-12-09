package instassist

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	version        = "1.0.0"
	defaultCLIName = "codex"
)

// Main is the entrypoint for the insta-assist application.
func Main() {
	cliFlag := flag.String("cli", defaultCLIName, "default CLI to use: codex, claude, gemini, or opencode")
	promptFlag := flag.String("prompt", "", "prompt to send (non-interactive mode)")
	selectFlag := flag.Int("select", -1, "auto-select option by index (0-based, use with -prompt)")
	outputFlag := flag.String("output", "clipboard", "output mode: clipboard, stdout, or exec")
	stayOpenExecFlag := flag.Bool("stay-open-exec", false, "when executing (Ctrl+R), keep the TUI open and show output instead of exiting")
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
	m := newModel(*cliFlag, *stayOpenExecFlag)
	if _, err := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}
