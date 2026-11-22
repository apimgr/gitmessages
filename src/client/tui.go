package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/apimgr/gitmessages/src/messages"
	tea "github.com/charmbracelet/bubbletea"
)

type terminalModel struct {
	server      string
	language    string
	config      *clientConfig
	setup       bool
	build       bool
	buildStep   int
	buildValues [3]string
	input       string
	selection   int
	output      string
	closed      bool
}

var terminalActions = []string{"Commit types", "Conventions", "Message templates", "Random message", "Build a commit message", "Exit"}

func runBubbleTUI(server, language string, config *clientConfig) error {
	model := terminalModel{server: server, language: language, config: config, setup: !validServer(server)}
	program := tea.NewProgram(model, tea.WithAltScreen())
	_, err := program.Run()
	return err
}

func launchInteractive(server, language string, config *clientConfig) error {
	mode := strings.ToLower(strings.TrimSpace(config.Display.Mode))
	if mode == "" {
		mode = "auto"
	}
	if mode != "auto" && mode != "gui" && mode != "tui" {
		return fmt.Errorf("invalid display.mode %q: expected auto, gui, or tui", mode)
	}
	guiAvailable := hasGUI()
	terminalAvailable := isInteractive()
	switch mode {
	case "gui":
		if !guiAvailable {
			return fmt.Errorf("GUI mode was requested but no desktop display is available")
		}
		return runGUI(server, language, config)
	case "tui":
		if !terminalAvailable {
			return fmt.Errorf("TUI mode was requested but stdin and stdout are not interactive terminals")
		}
		return runBubbleTUI(server, language, config)
	default:
		if guiAvailable {
			return runGUI(server, language, config)
		}
		if terminalAvailable {
			return runBubbleTUI(server, language, config)
		}
		return fmt.Errorf("interactive mode requires a desktop display or terminal")
	}
}

func hasGUI() bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return true
	default:
		return strings.TrimSpace(os.Getenv("DISPLAY")) != "" || strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
	}
}

func (m terminalModel) Init() tea.Cmd { return nil }

func (m terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := message.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	if key.Type == tea.KeyCtrlC || key.Type == tea.KeyEsc {
		if m.build || m.setup {
			m.build, m.setup = false, false
			m.input = ""
			return m, nil
		}
		m.closed = true
		return m, tea.Quit
	}
	if !m.setup && !m.build && key.Type == tea.KeyRunes && strings.EqualFold(string(key.Runes), "q") {
		m.closed = true
		return m, tea.Quit
	}
	if m.setup {
		switch key.Type {
		case tea.KeyEnter:
			server := strings.TrimSpace(m.input)
			if !validServer(server) {
				m.output = "Enter a valid http or https server URL."
				return m, nil
			}
			if _, err := request(server, "GET", "/api/v1/server/healthz", nil, m.language); err != nil {
				m.output = "Connection failed: " + err.Error()
				return m, nil
			}
			m.server = server
			m.config.Server.Primary = server
			if err := saveConfig(m.config); err != nil {
				m.output = err.Error()
				return m, nil
			}
			m.setup, m.input, m.output = false, "", "Connected and saved the server profile."
		case tea.KeyBackspace:
			m.input = trimLastRune(m.input)
		case tea.KeyRunes:
			m.input += string(key.Runes)
		}
		return m, nil
	}
	if m.build {
		switch key.Type {
		case tea.KeyEnter:
			if m.buildStep < len(m.buildValues) {
				m.buildValues[m.buildStep] = strings.TrimSpace(m.input)
				m.input = ""
				m.buildStep++
				if m.buildStep < len(m.buildValues) {
					return m, nil
				}
			}
			input := messages.BuildInput{Type: m.buildValues[0], Scope: m.buildValues[1], Description: m.buildValues[2], Convention: "conventional-commits"}
			if _, err := messages.Build(input); err != nil {
				m.output = err.Error()
			} else if payload, err := json.Marshal(input); err != nil {
				m.output = err.Error()
			} else if data, err := request(m.server, "POST", "/api/v1/messages", payload, m.language); err != nil {
				m.output = err.Error()
			} else {
				var result struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(data, &result); err != nil {
					m.output = err.Error()
				} else {
					m.output = result.Message
				}
			}
			m.build, m.buildStep = false, 0
		case tea.KeyBackspace:
			m.input = trimLastRune(m.input)
		case tea.KeyRunes:
			m.input += string(key.Runes)
		}
		return m, nil
	}
	switch key.Type {
	case tea.KeyUp:
		if m.selection > 0 {
			m.selection--
		}
	case tea.KeyDown:
		if m.selection < len(terminalActions)-1 {
			m.selection++
		}
	case tea.KeyEnter:
		switch m.selection {
		case 0, 1, 2, 3:
			paths := []string{"/api/v1/types", "/api/v1/conventions", "/api/v1/templates", "/api/v1/messages/random"}
			data, err := request(m.server, "GET", paths[m.selection], nil, m.language)
			if err != nil {
				m.output = err.Error()
			} else if m.selection == 3 {
				var result struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(data, &result); err != nil {
					m.output = err.Error()
				} else {
					m.output = result.Message
				}
			} else {
				var formatted bytes.Buffer
				if err := json.Indent(&formatted, data, "", "  "); err != nil {
					m.output = err.Error()
				} else {
					m.output = formatted.String()
				}
			}
		case 4:
			m.build, m.input, m.buildStep = true, "", 0
		case 5:
			m.closed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m terminalModel) View() string {
	if m.closed {
		return ""
	}
	if m.setup {
		return "Git Messages setup\n\nServer URL: " + m.input + "_\n\nEnter to connect and save · Esc to exit\n\n" + m.output
	}
	if m.build {
		prompts := []string{"Commit type", "Scope (optional)", "Description"}
		return fmt.Sprintf("Build a commit message\n\n%s: %s_\n\nEnter to continue · Esc to cancel\n\n%s", prompts[m.buildStep], m.input, m.output)
	}
	var view strings.Builder
	fmt.Fprintf(&view, "Git Messages  ·  %s\n\n", m.server)
	for index, action := range terminalActions {
		marker := "  "
		if index == m.selection {
			marker = "> "
		}
		fmt.Fprintf(&view, "%s%s\n", marker, action)
	}
	if m.output != "" {
		fmt.Fprintf(&view, "\n%s\n", m.output)
	}
	view.WriteString("\n↑/↓ select · Enter open · q exit")
	return view.String()
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
}
