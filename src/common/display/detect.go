package display

import (
	"os"
	"strings"

	"golang.org/x/term"
)

type DisplayMode int

const (
	DisplayModeHeadless DisplayMode = iota
	DisplayModeCLI
	DisplayModeTUI
	DisplayModeGUI
)

type DisplayEnv struct {
	Mode         DisplayMode
	HasDisplay   bool
	DisplayType  string
	IsTerminal   bool
	IsSSH        bool
	IsMosh       bool
	IsScreen     bool
	TerminalType string
	Cols         int
	Rows         int
}

func DetectDisplayEnv() DisplayEnv {
	environment := DisplayEnv{TerminalType: os.Getenv("TERM")}
	environment.IsTerminal = term.IsTerminal(int(os.Stdout.Fd()))
	if environment.IsTerminal {
		environment.Cols, environment.Rows, _ = term.GetSize(int(os.Stdout.Fd()))
	}
	environment.IsSSH = os.Getenv("SSH_CLIENT") != "" || os.Getenv("SSH_TTY") != "" || os.Getenv("SSH_CONNECTION") != ""
	environment.IsMosh = os.Getenv("MOSH") != "" || os.Getenv("MOSH_CONNECTION") != "" || strings.Contains(environment.TerminalType, "mosh")
	environment.IsScreen = os.Getenv("STY") != "" || os.Getenv("TMUX") != ""
	environment.detectPlatformDisplay()
	environment.Mode = environment.autoDetectDisplayMode()
	return environment
}

func (environment *DisplayEnv) autoDetectDisplayMode() DisplayMode {
	if !environment.IsTerminal && !environment.HasDisplay {
		return DisplayModeHeadless
	}
	if environment.HasDisplay && !environment.IsSSH && !environment.IsMosh {
		return DisplayModeGUI
	}
	if environment.TerminalType == "dumb" {
		return DisplayModeCLI
	}
	if environment.IsTerminal {
		return DisplayModeTUI
	}
	return DisplayModeCLI
}

func (environment *DisplayEnv) IsDumbTerminal() bool {
	return environment.TerminalType == "dumb"
}

func (environment DisplayEnv) IsAutoDetectDisplayModeGUI() bool {
	return environment.Mode == DisplayModeGUI
}

func (environment DisplayEnv) IsAutoDetectDisplayModeTUI() bool {
	return environment.Mode == DisplayModeTUI
}

func (environment DisplayEnv) IsAutoDetectDisplayModeCLI() bool {
	return environment.Mode == DisplayModeCLI
}

func (environment DisplayEnv) IsAutoDetectDisplayModeHeadless() bool {
	return environment.Mode == DisplayModeHeadless
}

func CanUseANSI(environment *DisplayEnv) bool {
	return environment != nil && !environment.IsDumbTerminal() && os.Getenv("NO_COLOR") == "" && environment.IsTerminal
}

func ColorEnabled(environment *DisplayEnv, override string) bool {
	switch strings.ToLower(strings.TrimSpace(override)) {
	case "yes":
		return true
	case "no":
		return false
	default:
		return CanUseANSI(environment)
	}
}

func EmojiEnabled(environment *DisplayEnv, override string) bool {
	return environment != nil && environment.IsTerminal && !environment.IsDumbTerminal() &&
		strings.ToLower(strings.TrimSpace(override)) != "no" && os.Getenv("NO_COLOR") == ""
}
