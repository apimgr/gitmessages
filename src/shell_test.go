package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stdout
	os.Stdout = write
	err = fn()
	_ = write.Close()
	os.Stdout = previous
	var output bytes.Buffer
	_, copyErr := io.Copy(&output, read)
	_ = read.Close()
	if copyErr != nil {
		t.Fatal(copyErr)
	}
	return output.String(), err
}

func TestPrintCompletions(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "sh", "dash", "ksh", "powershell", "pwsh"} {
		t.Run(shell, func(t *testing.T) {
			output, err := captureStdout(t, func() error { return printCompletions(shell, "gitmessages") })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, "--help") || !strings.Contains(output, "--version") {
				t.Fatalf("completion output missing shared flags: %q", output)
			}
		})
	}
}

func TestPrintShellInit(t *testing.T) {
	for _, test := range []struct {
		shell string
		want  string
	}{{"bash", "source <('gitmessages' --shell completions bash)"}, {"zsh", "source <('gitmessages' --shell completions zsh)"}, {"fish", "'gitmessages' --shell completions fish | source"}, {"sh", "eval \"$('gitmessages' --shell completions sh)\""}, {"powershell", "Invoke-Expression (& 'gitmessages' --shell completions powershell)"}} {
		t.Run(test.shell, func(t *testing.T) {
			output, err := captureStdout(t, func() error { return printShellInit(test.shell, "gitmessages") })
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output, test.want) {
				t.Fatalf("init output = %q, want it to contain %q", output, test.want)
			}
		})
	}
}

func TestShellIntegrationRejectsUnknownValues(t *testing.T) {
	if err := printCompletions("unknown", "gitmessages"); err == nil {
		t.Fatal("expected unsupported shell error")
	}
	if err := printShellInit("unknown", "gitmessages"); err == nil {
		t.Fatal("expected unsupported shell error")
	}
}

func TestPrintVersionHasStableMetadataLines(t *testing.T) {
	oldVersion, oldCommit, oldEpoch := Version, CommitID, BuildEpoch
	Version, CommitID, BuildEpoch = "1.2.3", "abc1234", "0"
	t.Cleanup(func() { Version, CommitID, BuildEpoch = oldVersion, oldCommit, oldEpoch })
	output, err := captureStdout(t, func() error { printVersion("renamed-server"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"renamed-server 1.2.3 (abc1234)", "Built: N/A", "Go: ", "OS/Arch: "} {
		if !strings.Contains(output, want) {
			t.Errorf("version output missing %q: %s", want, output)
		}
	}
}
