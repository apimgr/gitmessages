package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var clientCommands = []string{"types", "list", "conventions", "templates", "random", "build", "--help", "--version", "--server", "--token", "--lang", "--json", "--debug", "--color", "--shell", "--update"}

func printClientCompletions(shell, binary string) error {
	commands := strings.Join(clientCommands, " ")
	name := filepath.Base(binary)
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return fmt.Errorf("cannot generate completions for unsafe binary name %q", name)
		}
	}
	switch shell {
	case "bash":
		fmt.Printf("complete -W '%s' -- '%s'\n", commands, name)
	case "zsh":
		fmt.Printf("#compdef %s\n_%s() { _arguments '*:command:(%s)' }\n", name, name, commands)
	case "fish":
		for _, command := range clientCommands {
			fmt.Printf("complete -c %s -f -a '%s'\n", name, command)
		}
		fmt.Printf("complete -c %s -f -l update -r -a 'check yes'\n", name)
	case "sh", "dash", "ksh":
		fmt.Printf("_%s_complete() {\n  printf '%%s\\n' %s\n}\n", name, clientShellQuote(strings.ReplaceAll(commands, " ", "\n")))
	case "powershell", "pwsh":
		fmt.Printf("Register-ArgumentCompleter -CommandName %s -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", name, commands)
	default:
		return fmt.Errorf("unsupported shell %q; supported shells: bash, zsh, fish, sh, dash, ksh, powershell, pwsh", shell)
	}
	return nil
}

func printClientShellIntegration(action string, args []string, binary string) error {
	if action == "help" || action == "--help" {
		fmt.Printf("Shell integration for %s:\n  --shell completions [SHELL]\n  --shell init [SHELL]\n  SHELL: bash, zsh, fish, sh, dash, ksh, powershell, pwsh (auto-detect if omitted)\n", binary)
		return nil
	}
	if action != "completions" && action != "init" {
		return fmt.Errorf("unknown shell integration %q; use --shell help", action)
	}
	if len(args) > 1 {
		return fmt.Errorf("unexpected shell integration arguments: %s", strings.Join(args[1:], " "))
	}
	shell := ""
	if len(args) > 0 {
		shell = strings.TrimSpace(args[0])
	}
	if shell == "" {
		shell = detectClientShell()
	}
	if action == "completions" {
		return printClientCompletions(shell, binary)
	}
	return printClientShellInit(shell, binary)
}

func detectClientShell() string {
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return filepath.Base(shell)
	}
	if shell := strings.TrimSpace(os.Getenv("ComSpec")); shell != "" {
		return "powershell"
	}
	return "bash"
}

func printClientShellInit(shell, binary string) error {
	name := filepath.Base(binary)
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' && char != '.' {
			return fmt.Errorf("cannot generate shell initialization for unsafe binary name %q", name)
		}
	}
	quoted := clientShellQuote(name)
	switch shell {
	case "bash":
		fmt.Printf("source <(%s --shell completions bash)\n", quoted)
	case "zsh":
		fmt.Printf("source <(%s --shell completions zsh)\n", quoted)
	case "fish":
		fmt.Printf("%s --shell completions fish | source\n", clientFishQuote(name))
	case "sh", "dash", "ksh":
		fmt.Printf("eval \"$(%s --shell completions %s)\"\n", quoted, shell)
	case "powershell", "pwsh":
		fmt.Printf("Invoke-Expression (& %s --shell completions powershell)\n", clientPowerShellQuote(name))
	default:
		return fmt.Errorf("unsupported shell %q; supported shells: bash, zsh, fish, sh, dash, ksh, powershell, pwsh", shell)
	}
	return nil
}

func clientShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func clientFishQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}

func clientPowerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
