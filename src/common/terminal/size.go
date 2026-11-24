package terminal

import (
	"os"

	"golang.org/x/term"
)

type SizeMode int

const (
	SizeModeMicro SizeMode = iota
	SizeModeMinimal
	SizeModeCompact
	SizeModeStandard
	SizeModeWide
	SizeModeUltrawide
	SizeModeMassive
)

type TerminalSize struct {
	Cols int
	Rows int
	Mode SizeMode
}

func GetTerminalSize() TerminalSize {
	cols, rows, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || cols < 1 {
		cols = 80
	}
	if err != nil || rows < 1 {
		rows = 24
	}
	return SizeForDimensions(cols, rows)
}

func SizeForDimensions(cols, rows int) TerminalSize {
	if cols < 1 {
		cols = 80
	}
	if rows < 1 {
		rows = 24
	}
	return TerminalSize{Cols: cols, Rows: rows, Mode: ModeForDimensions(cols, rows)}
}

func ModeForDimensions(cols, rows int) SizeMode {
	switch {
	case cols < 40 || rows < 10:
		return SizeModeMicro
	case cols < 60 || rows < 16:
		return SizeModeMinimal
	case cols < 80 || rows < 24:
		return SizeModeCompact
	case cols < 120 || rows < 40:
		return SizeModeStandard
	case cols < 200 || rows < 60:
		return SizeModeWide
	case cols < 400 || rows < 80:
		return SizeModeUltrawide
	default:
		return SizeModeMassive
	}
}

func (mode SizeMode) ShowASCIIArt() bool { return mode >= SizeModeStandard }
func (mode SizeMode) ShowBorders() bool  { return mode >= SizeModeCompact }
func (mode SizeMode) ShowSidebar() bool  { return mode >= SizeModeWide }
func (mode SizeMode) ShowIcons() bool    { return mode >= SizeModeMinimal }
