package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

var clientCommands = []string{"types", "list", "conventions", "templates", "random", "build", "--help", "--version", "--server", "--lang", "--json", "--debug", "--color", "--shell"}

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
	case "powershell", "pwsh":
		fmt.Printf("Register-ArgumentCompleter -CommandName %s -ScriptBlock { param($wordToComplete) '%s'.Split(' ') | Where-Object { $_ -like \"$wordToComplete*\" } }\n", name, commands)
	default:
		return fmt.Errorf("unsupported shell %q; use bash, zsh, fish, or powershell", shell)
	}
	return nil
}
