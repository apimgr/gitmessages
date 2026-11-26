package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/apimgr/gitmessages/src/common/display"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/common/terminal"
	"github.com/apimgr/gitmessages/src/messages"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type terminalModel struct {
	server       string
	language     string
	config       *clientConfig
	setup        bool
	build        bool
	buildStep    int
	buildValues  [3]string
	input        string
	query        string
	selection    int
	scrollOffset int
	outputOffset int
	output       string
	width        int
	height       int
	searching    bool
	help         bool
	closed       bool
}

var terminalActionKeys = []string{"gui.types", "gui.conventions", "gui.templates", "gui.random", "builder.title", "common.exit"}

func runBubbleTUI(server, language string, config *clientConfig) error {
	size := terminal.GetTerminalSize()
	model := terminalModel{server: server, language: language, config: config, setup: !validServer(server), width: size.Cols, height: size.Rows}
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
		return errors.New(i18n.Format(language, "cli.invalid_display_mode", map[string]string{"mode": mode}))
	}
	guiAvailable := hasGUI()
	terminalAvailable := isInteractive()
	switch mode {
	case "gui":
		if !guiAvailable {
			return errors.New(i18n.T(language, "cli.gui_unavailable"))
		}
		return runGUI(server, language, config)
	case "tui":
		if !terminalAvailable {
			return errors.New(i18n.T(language, "cli.tui_unavailable"))
		}
		return runBubbleTUI(server, language, config)
	default:
		if guiAvailable {
			return runGUI(server, language, config)
		}
		if terminalAvailable {
			return runBubbleTUI(server, language, config)
		}
		return errors.New(i18n.T(language, "cli.interactive_required"))
	}
}

func hasGUI() bool {
	return guiSupported() && display.DetectDisplayEnv().IsAutoDetectDisplayModeGUI()
}

func (m terminalModel) Init() tea.Cmd { return nil }

func (m terminalModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = message.Width, message.Height
		m.ensureVisible()
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(message)
	default:
		return m, nil
	}
}

func (m terminalModel) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.closed {
		return m, tea.Quit
	}
	if key.Type == tea.KeyCtrlC {
		m.closed = true
		return m, tea.Quit
	}
	if m.help {
		if key.Type == tea.KeyEsc || key.String() == "?" || key.String() == "q" {
			m.help = false
		}
		return m, nil
	}
	if m.searching {
		switch key.Type {
		case tea.KeyEsc:
			m.searching = false
		case tea.KeyEnter:
			m.searching = false
		case tea.KeyBackspace:
			m.query = trimLastRune(m.query)
			m.selection, m.scrollOffset = 0, 0
		default:
			if key.Type == tea.KeyRunes {
				m.query += string(key.Runes)
				m.selection, m.scrollOffset = 0, 0
			}
		}
		m.ensureVisible()
		return m, nil
	}
	if key.Type == tea.KeyEsc {
		if m.build || m.setup {
			m.build, m.setup = false, false
			m.input = ""
			return m, nil
		}
		m.closed = true
		return m, tea.Quit
	}
	if m.setup {
		return m.updateSetup(key)
	}
	if m.build {
		return m.updateBuild(key)
	}
	if key.Type == tea.KeyRunes {
		switch key.String() {
		case "q":
			m.closed = true
			return m, tea.Quit
		case "?":
			m.help = true
			return m, nil
		case "/":
			m.searching = true
			m.query = ""
			m.selection, m.scrollOffset = 0, 0
			return m, nil
		case "j":
			m.moveSelection(1)
		case "k":
			m.moveSelection(-1)
		case "g":
			m.selection, m.scrollOffset = 0, 0
		case "G":
			m.selection = max(0, len(m.filteredActions())-1)
			m.ensureVisible()
		}
	}
	switch key.Type {
	case tea.KeyUp:
		m.moveSelection(-1)
	case tea.KeyDown:
		m.moveSelection(1)
	case tea.KeyHome:
		m.selection, m.scrollOffset = 0, 0
	case tea.KeyEnd:
		m.selection = max(0, len(m.filteredActions())-1)
		m.ensureVisible()
	case tea.KeyPgUp:
		if m.output != "" {
			m.outputOffset = max(0, m.outputOffset-m.pageSize())
		} else {
			m.moveSelection(-m.pageSize())
		}
	case tea.KeyPgDown:
		if m.output != "" {
			m.outputOffset = min(max(0, len(strings.Split(m.output, "\n"))-1), m.outputOffset+m.pageSize())
		} else {
			m.moveSelection(m.pageSize())
		}
	case tea.KeyEnter:
		m.activateSelection()
		if m.closed {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m terminalModel) updateSetup(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.Type {
	case tea.KeyEnter:
		server := strings.TrimSpace(m.input)
		if !validServer(server) {
			m.output = i18n.T(m.language, "gui.invalid_server")
			return m, nil
		}
		apiVersion, err := discoverAPIVersion(server, m.language)
		if err != nil {
			m.output = i18n.Format(m.language, "gui.connection_failed", map[string]string{"error": err.Error()})
			return m, nil
		}
		if _, err := request(server, "GET", "/api/{api_version}/server/healthz", nil, m.language); err != nil {
			m.output = i18n.Format(m.language, "gui.connection_failed", map[string]string{"error": err.Error()})
			return m, nil
		}
		m.server = server
		m.config.Server.Primary = server
		m.config.Server.APIVersion = apiVersion
		if err := saveConfig(m.config); err != nil {
			m.output = i18n.Format(m.language, "gui.profile_save_failed", map[string]string{"error": err.Error()})
			return m, nil
		}
		m.setup, m.input, m.output = false, "", i18n.T(m.language, "gui.connected")
	case tea.KeyBackspace:
		m.input = trimLastRune(m.input)
	case tea.KeyRunes:
		m.input += string(key.Runes)
	}
	return m, nil
}

func (m terminalModel) updateBuild(key tea.KeyMsg) (tea.Model, tea.Cmd) {
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
			m.output = localizeClientError(m.language, err)
		} else if payload, err := json.Marshal(input); err != nil {
			m.output = localizeClientError(m.language, err)
		} else if data, err := request(m.server, "POST", "/api/{api_version}/messages", payload, m.language); err != nil {
			m.output = localizeClientError(m.language, err)
		} else {
			var result struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				m.output = localizeClientError(m.language, err)
			} else {
				m.output = result.Message
			}
		}
		m.build, m.buildStep, m.outputOffset = false, 0, 0
	case tea.KeyBackspace:
		m.input = trimLastRune(m.input)
	case tea.KeyRunes:
		m.input += string(key.Runes)
	}
	return m, nil
}

func (m *terminalModel) moveSelection(delta int) {
	count := len(m.filteredActions())
	if count == 0 {
		m.selection, m.scrollOffset = 0, 0
		return
	}
	m.selection = max(0, min(count-1, m.selection+delta))
	m.ensureVisible()
}

func (m *terminalModel) ensureVisible() {
	count := len(m.filteredActions())
	if count == 0 {
		m.selection, m.scrollOffset = 0, 0
		return
	}
	m.selection = max(0, min(count-1, m.selection))
	visible := max(1, m.dimensions().Rows-7)
	if m.selection < m.scrollOffset {
		m.scrollOffset = m.selection
	}
	if m.selection >= m.scrollOffset+visible {
		m.scrollOffset = m.selection - visible + 1
	}
}

func (m terminalModel) filteredActions() []int {
	var actions []int
	query := strings.ToLower(strings.TrimSpace(m.query))
	for index, key := range terminalActionKeys {
		if query == "" || strings.Contains(strings.ToLower(i18n.T(m.language, key)), query) {
			actions = append(actions, index)
		}
	}
	return actions
}

func (m *terminalModel) activateSelection() {
	actions := m.filteredActions()
	if m.selection < 0 || m.selection >= len(actions) {
		return
	}
	selected := actions[m.selection]
	if selected == 4 {
		m.build, m.input, m.buildStep = true, "", 0
		return
	}
	if selected == 5 {
		m.closed = true
		return
	}
	paths := []string{"/api/{api_version}/types", "/api/{api_version}/conventions", "/api/{api_version}/templates", "/api/{api_version}/messages/random"}
	if !validServer(m.server) {
		m.output = i18n.T(m.language, "gui.connect_first")
		return
	}
	data, err := request(m.server, "GET", paths[selected], nil, m.language)
	if err != nil {
		m.output = localizeClientError(m.language, err)
		return
	}
	if selected == 3 {
		var result struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			m.output = localizeClientError(m.language, err)
		} else {
			m.output = result.Message
		}
	} else {
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, data, "", "  "); err != nil {
			m.output = localizeClientError(m.language, err)
		} else {
			m.output = formatted.String()
		}
	}
	m.outputOffset = 0
}

func (m terminalModel) dimensions() terminal.TerminalSize {
	return terminal.SizeForDimensions(m.width, m.height)
}

func (m terminalModel) pageSize() int {
	return max(1, m.dimensions().Rows-4)
}

func (m terminalModel) View() string {
	if m.closed {
		return ""
	}
	width, height := m.dimensions().Cols, m.dimensions().Rows
	if m.help {
		lines := []string{i18n.T(m.language, "tui.help_title"), "", i18n.T(m.language, "tui.help_navigation"), i18n.T(m.language, "tui.help_search"), i18n.T(m.language, "tui.help_open"), i18n.T(m.language, "tui.help_close"), i18n.T(m.language, "tui.help_quit")}
		return fitTerminalLines(lines, width, height)
	}
	if m.setup {
		lines := []string{i18n.T(m.language, "tui.setup_title"), "", i18n.T(m.language, "tui.server_url") + ": " + m.input + "_", "", i18n.T(m.language, "tui.connect_hint") + " · " + i18n.T(m.language, "tui.exit_hint"), m.output}
		return fitTerminalLines(lines, width, height)
	}
	if m.build {
		prompts := []string{i18n.T(m.language, "builder.type"), i18n.T(m.language, "builder.scope"), i18n.T(m.language, "builder.description")}
		lines := []string{i18n.T(m.language, "builder.title"), "", prompts[m.buildStep] + ": " + m.input + "_", "", i18n.T(m.language, "tui.continue_hint") + " · " + i18n.T(m.language, "tui.cancel_hint"), m.output}
		return fitTerminalLines(lines, width, height)
	}
	mode := m.dimensions().Mode
	lines := make([]string, 0, height)
	if mode > terminal.SizeModeMicro {
		lines = append(lines, truncateTerminalLine(i18n.T(m.language, "home.title")+" · "+m.server, width))
	} else {
		lines = append(lines, truncateTerminalLine(m.server, width))
	}
	if m.searching {
		lines = append(lines, truncateTerminalLine(i18n.Format(m.language, "tui.search_prompt", map[string]string{"query": m.query}), width))
	} else if strings.TrimSpace(m.query) != "" {
		lines = append(lines, truncateTerminalLine(i18n.Format(m.language, "tui.search_active", map[string]string{"query": m.query}), width))
	}
	actions := m.filteredActions()
	visibleCount := max(1, height-len(lines)-5)
	start := min(m.scrollOffset, max(0, len(actions)-1))
	end := min(len(actions), start+visibleCount)
	for index := start; index < end; index++ {
		marker := "  "
		if index == m.selection {
			marker = "> "
		}
		lines = append(lines, truncateTerminalLine(marker+i18n.T(m.language, terminalActionKeys[actions[index]]), width))
	}
	if len(actions) == 0 {
		lines = append(lines, truncateTerminalLine(i18n.T(m.language, "tui.search_empty"), width))
	}
	if m.output != "" && len(lines) < height-1 {
		output := strings.Split(m.output, "\n")
		available := min(3, max(0, height-len(lines)-1))
		if m.outputOffset >= len(output) {
			m.outputOffset = max(0, len(output)-1)
		}
		endOutput := min(len(output), m.outputOffset+available)
		for _, line := range output[m.outputOffset:endOutput] {
			lines = append(lines, truncateTerminalLine(line, width))
		}
	}
	hint := i18n.T(m.language, "tui.select_hint") + " · " + i18n.T(m.language, "tui.exit_hint")
	if mode == terminal.SizeModeMicro {
		hint = i18n.T(m.language, "tui.micro_hint")
	}
	lines = append(lines, truncateTerminalLine(hint, width))
	return fitTerminalLines(lines, width, height)
}

func fitTerminalLines(lines []string, width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for index, line := range lines {
		lines[index] = truncateTerminalLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func truncateTerminalLine(line string, width int) string {
	if runewidth.StringWidth(line) <= width {
		return line
	}
	if width < 2 {
		for _, char := range line {
			return string(char)
		}
		return ""
	}
	limit := width - 1
	var output strings.Builder
	used := 0
	for _, char := range line {
		charWidth := runewidth.RuneWidth(char)
		if used+charWidth > limit {
			break
		}
		output.WriteRune(char)
		used += charWidth
	}
	return output.String() + "…"
}

func trimLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	return string(runes[:len(runes)-1])
}
