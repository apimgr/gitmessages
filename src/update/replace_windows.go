//go:build windows

package update

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

func replaceAndRestart(executable, stagedPath string) error {
	helperStage, err := os.CreateTemp(filepath.Dir(executable), ".gitmessages-update-helper-*.exe")
	if err != nil {
		return fmt.Errorf("create helper update copy: %w", err)
	}
	helperStagePath := helperStage.Name()
	if err := helperStage.Close(); err != nil {
		_ = os.Remove(helperStagePath)
		return err
	}
	if err := os.Remove(helperStagePath); err != nil {
		return err
	}
	if err := copyFile(stagedPath, helperStagePath); err != nil {
		return fmt.Errorf("copy update for Windows replacement: %w", err)
	}

	arguments := stripUpdateArguments(os.Args)
	argumentList := make([]string, 0, len(arguments)-1)
	for _, argument := range arguments[1:] {
		quoted := quoteWindowsArgument(argument)
		argumentList = append(argumentList, "'"+strings.ReplaceAll(quoted, "'", "''")+"'")
	}
	processArguments := "@(" + strings.Join(argumentList, ",") + ")"
	script := fmt.Sprintf(`$ErrorActionPreference = 'Stop'
$target = '%s'
$stage = '%s'
$pidToWait = %d
while (Get-Process -Id $pidToWait -ErrorAction SilentlyContinue) { Start-Sleep -Milliseconds 100 }
[System.IO.File]::Replace($stage, $target, $null)
Start-Process -FilePath $target -ArgumentList %s`,
		strings.ReplaceAll(executable, "'", "''"),
		strings.ReplaceAll(helperStagePath, "'", "''"),
		os.Getpid(), processArguments)
	encoded := base64.StdEncoding.EncodeToString(encodeUTF16LE(script))
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-EncodedCommand", encoded)
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
	if err := command.Start(); err != nil {
		_ = os.Remove(helperStagePath)
		return fmt.Errorf("start Windows update helper: %w", err)
	}
	return nil
}

func quoteWindowsArgument(argument string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	backslashes := 0
	for _, character := range argument {
		if character == '\\' {
			backslashes++
			continue
		}
		if character == '"' {
			quoted.WriteString(strings.Repeat("\\", backslashes*2+1))
			quoted.WriteRune(character)
			backslashes = 0
			continue
		}
		quoted.WriteString(strings.Repeat("\\", backslashes))
		quoted.WriteRune(character)
		backslashes = 0
	}
	quoted.WriteString(strings.Repeat("\\", backslashes*2))
	quoted.WriteByte('"')
	return quoted.String()
}

func encodeUTF16LE(value string) []byte {
	encoded := make([]byte, 0, len(value)*2)
	for _, character := range value {
		if character <= 0xffff {
			encoded = append(encoded, byte(character), byte(character>>8))
			continue
		}
		character -= 0x10000
		high := rune(0xd800) + (character >> 10)
		low := rune(0xdc00) + (character & 0x3ff)
		encoded = append(encoded, byte(high), byte(high>>8), byte(low), byte(low>>8))
	}
	return encoded
}

func copyFile(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := output.ReadFrom(input); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := output.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}
