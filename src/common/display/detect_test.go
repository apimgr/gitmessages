package display

import "testing"

func TestAutoDetectDisplayMode(t *testing.T) {
	tests := []struct {
		name string
		env  DisplayEnv
		want DisplayMode
	}{
		{name: "headless", want: DisplayModeHeadless},
		{name: "terminal", env: DisplayEnv{IsTerminal: true, TerminalType: "xterm"}, want: DisplayModeTUI},
		{name: "display", env: DisplayEnv{HasDisplay: true}, want: DisplayModeGUI},
		{name: "display wins over dumb terminal", env: DisplayEnv{HasDisplay: true, IsTerminal: true, TerminalType: "dumb"}, want: DisplayModeGUI},
		{name: "remote display", env: DisplayEnv{HasDisplay: true, IsTerminal: true, IsSSH: true}, want: DisplayModeTUI},
		{name: "dumb terminal", env: DisplayEnv{IsTerminal: true, TerminalType: "dumb"}, want: DisplayModeCLI},
		{name: "nonterminal output", env: DisplayEnv{IsTerminal: false}, want: DisplayModeHeadless},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.env.autoDetectDisplayMode(); got != test.want {
				t.Fatalf("autoDetectDisplayMode() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestCanUseANSI(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	if !CanUseANSI(&DisplayEnv{IsTerminal: true, TerminalType: "xterm"}) {
		t.Fatal("ANSI should be enabled for a capable terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if CanUseANSI(&DisplayEnv{IsTerminal: true, TerminalType: "xterm"}) {
		t.Fatal("NO_COLOR should disable ANSI")
	}
	t.Setenv("NO_COLOR", "")
	if CanUseANSI(&DisplayEnv{IsTerminal: true, TerminalType: "dumb"}) {
		t.Fatal("TERM=dumb should disable ANSI")
	}
	if CanUseANSI(&DisplayEnv{IsTerminal: false, TerminalType: "xterm"}) {
		t.Fatal("nonterminal output should disable ANSI")
	}
}

func TestColorAndEmojiOverrides(t *testing.T) {
	environment := &DisplayEnv{IsTerminal: true, TerminalType: "dumb"}
	t.Setenv("NO_COLOR", "1")
	if !ColorEnabled(environment, "yes") {
		t.Fatal("explicit color=yes should enable color sequences")
	}
	if ColorEnabled(environment, "no") {
		t.Fatal("explicit color=no should disable color sequences")
	}
	if ColorEnabled(environment, "auto") {
		t.Fatal("auto color should respect NO_COLOR and TERM=dumb")
	}
	if EmojiEnabled(environment, "yes") {
		t.Fatal("color=yes must not enable emojis in a dumb terminal")
	}
	t.Setenv("NO_COLOR", "")
	environment.TerminalType = "xterm"
	if !EmojiEnabled(environment, "auto") {
		t.Fatal("emoji should be enabled in a capable terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if !EmojiEnabled(environment, "yes") {
		t.Fatal("explicit emoji=true should override NO_COLOR")
	}
	if EmojiEnabled(environment, "auto") {
		t.Fatal("automatic emoji should respect NO_COLOR")
	}
	environment.TerminalType = "dumb"
	if EmojiEnabled(environment, "yes") {
		t.Fatal("TERM=dumb must disable emoji even with an explicit override")
	}
	environment.TerminalType = "xterm"
	t.Setenv("NO_COLOR", "")
	if EmojiEnabled(environment, "no") {
		t.Fatal("color=no should disable decorative output")
	}
}
