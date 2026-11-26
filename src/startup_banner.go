package main

import (
	"fmt"
	"io"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/apimgr/gitmessages/src/common/display"
	"github.com/mattn/go-runewidth"
)

func writeStartupBanner(output io.Writer, environment display.DisplayEnv, colorMode, version, commit, built, mode, publicURL string) error {
	width := environment.Cols
	if width <= 0 {
		width = 80
	}
	lines := []string{"Git Messages " + version, "Commit: " + commit, "Built: " + built, "Mode: " + mode}
	if publicURL != "" {
		lines = append(lines, "URL: "+publicURL)
	}
	switch {
	case width >= 80:
		unicode := display.EmojiEnabled(&environment, colorMode)
		color := display.ColorEnabled(&environment, colorMode)
		topLeft, topRight, bottomLeft, bottomRight, vertical, horizontal := "+", "+", "+", "+", "|", "-"
		if unicode {
			topLeft, topRight, bottomLeft, bottomRight, vertical, horizontal = "╭", "╮", "╰", "╯", "│", "─"
		}
		if _, err := fmt.Fprintln(output, topLeft+strings.Repeat(horizontal, width-2)+topRight); err != nil {
			return err
		}
		for _, line := range lines {
			if unicode && strings.HasPrefix(line, "Git Messages") {
				line = "🚀 " + line
			}
			line = truncateBannerLine(line, width-4)
			if color && strings.HasPrefix(line, "Git Messages") {
				line = "\x1b[1;36m" + line + "\x1b[0m"
			} else if color && strings.HasPrefix(line, "🚀 Git Messages") {
				line = "\x1b[1;36m" + line + "\x1b[0m"
			}
			padding := width - 4 - runewidth.StringWidth(stripANSI(line))
			if padding < 0 {
				padding = 0
			}
			if _, err := fmt.Fprintf(output, "%s %s%s %s\n", vertical, line, strings.Repeat(" ", padding), vertical); err != nil {
				return err
			}
		}
		_, err := fmt.Fprintln(output, bottomLeft+strings.Repeat(horizontal, width-2)+bottomRight)
		return err
	case width >= 60:
		for _, line := range lines {
			if _, err := fmt.Fprintln(output, truncateBannerLine(line, width)); err != nil {
				return err
			}
		}
		return nil
	case width >= 40:
		line := "Git Messages " + version
		if _, err := fmt.Fprintln(output, truncateBannerLine(line, width)); err != nil {
			return err
		}
		if publicURL != "" {
			_, err := fmt.Fprintln(output, truncateBannerLine(publicURL, width))
			return err
		}
		return nil
	default:
		return writeSingleLine(output, "Git Messages "+version, width)
	}
}

func stripANSI(value string) string {
	for _, sequence := range []string{"\x1b[1;36m", "\x1b[0m"} {
		value = strings.ReplaceAll(value, sequence, "")
	}
	return value
}

func truncateBannerLine(value string, width int) string {
	if width < 1 || len(value) <= width {
		return value
	}
	if width == 1 {
		return value[:1]
	}
	if width >= 4 {
		return value[:width-3] + "..."
	}
	return value[:width]
}

func writeSingleLine(output io.Writer, value string, width int) error {
	_, err := fmt.Fprintln(output, truncateBannerLine(value, width))
	return err
}

func startupPublicURL(fqdn, baseURL string, listeners []serverListener) string {
	host := strings.TrimSpace(fqdn)
	if host == "" || len(listeners) == 0 {
		return ""
	}
	selected := listeners[0]
	for _, listener := range listeners {
		if listener.tls {
			selected = listener
			break
		}
	}
	scheme := "http"
	if selected.tls {
		scheme = "https"
	}
	if (scheme == "http" && selected.port != 80) || (scheme == "https" && selected.port != 443) {
		host = net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(selected.port))
	}
	return (&url.URL{Scheme: scheme, Host: host, Path: baseURL}).String()
}
