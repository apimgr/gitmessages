package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var shellCommands = []string{"--help", "--version", "--status", "--port", "--address", "--config", "--mode", "--update", "--service", "--maintenance", "--shell", "--debug", "--color", "--lang"}

func printShellIntegration(command, shell string) error {
	if command == "help" || command == "--help" {
		fmt.Printf("Shell integration for %s:\n  --shell completions [SHELL]\n  --shell init [SHELL]\n  SHELL: bash, zsh, fish, sh, dash, ksh, powershell, pwsh (auto-detect if omitted)\n", filepath.Base(os.Args[0]))
		return nil
	}
	if shell == "" {
		shell = detectShell()
	}
	binary := filepath.Base(os.Args[0])
	switch command {
	case "completions":
		return printCompletions(shell, binary)
	case "init":
		return printShellInit(shell, binary)
	default:
		return fmt.Errorf("unknown shell integration %q; use --shell help", command)
	}
}

func detectShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return filepath.Base(shell)
	}
	if shell := strings.TrimSpace(os.Getenv("ComSpec")); shell != "" {
		return "powershell"
	}
	return "bash"
}

func printCompletions(shell, binary string) error {
	commands := strings.Join(shellCommands, " ")
	quoted := shellSingleQuote(binary)
	switch shell {
	case "bash":
		fmt.Printf("complete -W %s -- %s\n", shellSingleQuote(commands), quoted)
	case "zsh":
		fmt.Printf("#compdef %s\n_%s() {\n  _arguments '*:command:(%s)'\n}\n", quoted, binary, commands)
	case "fish":
		for _, command := range shellCommands {
			fmt.Printf("complete -c %s -f -a %s\n", fishQuote(binary), fishQuote(command))
		}
	case "sh", "dash", "ksh":
		fmt.Printf("_%s_complete() {\n  printf '%%s\\n' %s\n}\n", binary, shellSingleQuote(strings.ReplaceAll(commands, " ", "\n")))
	case "powershell", "pwsh":
		fmt.Printf("Register-ArgumentCompleter -CommandName %s -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", powershellQuote(binary), commands)
	default:
		return fmt.Errorf("unsupported shell %q; supported shells: bash, zsh, fish, sh, dash, ksh, powershell, pwsh", shell)
	}
	return nil
}

func printShellInit(shell, binary string) error {
	quoted := shellSingleQuote(binary)
	switch shell {
	case "bash":
		fmt.Printf("source <(%s --shell completions bash)\n", quoted)
	case "zsh":
		fmt.Printf("source <(%s --shell completions zsh)\n", quoted)
	case "fish":
		fmt.Printf("%s --shell completions fish | source\n", fishQuote(binary))
	case "sh", "dash", "ksh":
		fmt.Printf("eval \"$(%s --shell completions %s)\"\n", quoted, shell)
	case "powershell", "pwsh":
		fmt.Printf("Invoke-Expression (& %s --shell completions powershell)\n", powershellQuote(binary))
	default:
		return fmt.Errorf("unsupported shell %q; supported shells: bash, zsh, fish, sh, dash, ksh, powershell, pwsh", shell)
	}
	return nil
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func fishQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
