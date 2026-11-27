package banner

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/common/display"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"golang.org/x/term"
	_ "time/tzdata"
)

var consoleWriteMu sync.Mutex

type synchronizedConsoleWriter struct {
	writer io.Writer
}

// SynchronizedConsoleWriter serializes logger output with the startup banner.
func SynchronizedConsoleWriter(writer io.Writer) io.Writer {
	return synchronizedConsoleWriter{writer: writer}
}

func (writer synchronizedConsoleWriter) Write(value []byte) (int, error) {
	consoleWriteMu.Lock()
	defer consoleWriteMu.Unlock()
	return writer.writer.Write(value)
}

// ServerBannerInfo contains the values resolved by server startup for its banner.
type ServerBannerInfo struct {
	AppName         string
	Version         string
	AppMode         string
	StartupDatetime string
	URLs            []string
	OnionAddress    string
	I2PAddress      string
	SMTPAddress     string
	SetupToken      string
	ForceColor      *bool
	ForceEmoji      *bool
	Lang            string
}

// PrintServerStartupBanner prints the responsive startup banner for the server binary.
func PrintServerStartupBanner(info ServerBannerInfo) {
	environment := display.DetectDisplayEnv()
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 1 {
		width = 80
	}
	colorMode := "auto"
	if info.ForceColor != nil {
		if *info.ForceColor {
			colorMode = "yes"
		} else {
			colorMode = "no"
		}
	}
	emojiMode := "auto"
	if info.ForceEmoji != nil {
		if *info.ForceEmoji {
			emojiMode = "yes"
		} else {
			emojiMode = "no"
		}
	}
	startupDatetime := info.StartupDatetime
	if startupDatetime == "" {
		startupDatetime = FormatStartupDatetime(time.Now(), os.Getenv("TZ"))
	}
	var output bytes.Buffer
	printServerStartupBanner(&output, info, startupDatetime, width,
		display.ColorEnabled(&environment, colorMode), display.EmojiEnabled(&environment, emojiMode))
	consoleWriteMu.Lock()
	defer consoleWriteMu.Unlock()
	_, _ = os.Stdout.Write(output.Bytes())
}

func printServerStartupBanner(output io.Writer, info ServerBannerInfo, startupDatetime string, width int, useColors, useEmojis bool) {
	if width < 1 {
		width = 80
	}
	if !useEmojis {
		fmt.Fprintf(output, "%s v%s\n", info.AppName, info.Version)
		fmt.Fprintf(output, "%s: %s\n", i18n.T(info.Lang, "cli.running_in_mode_label"), info.AppMode)
		printServerBannerOverlays(output, info, false)
		for _, address := range info.URLs {
			fmt.Fprintf(output, "  %s: %s\n", i18n.T(info.Lang, "cli.listening_on_label"), address)
		}
		fmt.Fprintf(output, "%s %s\n", i18n.T(info.Lang, "cli.server_started_on_label"), startupDatetime)
		fmt.Fprintln(output)
		return
	}

	switch {
	case width >= 80:
		printServerBannerFull(output, info, startupDatetime, useColors)
	case width >= 60:
		printServerBannerCompact(output, info, startupDatetime, useColors)
	case width >= 40:
		fmt.Fprintf(output, "%s %s\n", info.AppName, info.Version)
		printServerBannerOverlays(output, info, false)
		for _, address := range info.URLs {
			fmt.Fprintln(output, extractHostPort(address))
		}
	default:
		if len(info.URLs) == 0 {
			fmt.Fprintln(output, info.AppName)
			return
		}
		fmt.Fprintf(output, "%s %s\n", info.AppName, extractHostPort(info.URLs[0]))
	}
}

func printServerBannerFull(output io.Writer, info ServerBannerInfo, startupDatetime string, useColors bool) {
	art := []string{
		"  ____ _ _     __  __                                  ",
		" / ___(_) |_  |  \\/  | ___  ___ ___  __ _  __ _  ___ ",
		"| |  _| | __| | |\\/| |/ _ \\/ __/ __|/ _` |/ _` |/ _ \\ ",
		"| |_| | | |_  | |  | |  __/\\__ \\__ \\ (_| | (_| |  __/",
		" \\____|_|\\__| |_|  |_|\\___||___/___/\\__,_|\\__, |\\___|",
		"                                             |___/       ",
	}
	for _, line := range art {
		if useColors {
			fmt.Fprintf(output, "\x1b[1;36m%s\x1b[0m\n", line)
		} else {
			fmt.Fprintln(output, line)
		}
	}
	fmt.Fprintln(output)
	if useColors {
		fmt.Fprintf(output, "🚀 \x1b[1;36m%s\x1b[0m v%s\n", info.AppName, info.Version)
	} else {
		fmt.Fprintf(output, "🚀 %s v%s\n", info.AppName, info.Version)
	}
	printServerBannerAppModeLine(output, info.AppMode, true, info.Lang)
	fmt.Fprintln(output)
	printServerBannerOverlays(output, info, true)
	for _, address := range info.URLs {
		fmt.Fprintf(output, "  🌐 %s: %s\n", i18n.T(info.Lang, "cli.listening_on_label"), address)
	}
	fmt.Fprintf(output, "✅ %s %s\n", i18n.T(info.Lang, "cli.server_started_on_label"), startupDatetime)
	fmt.Fprintln(output)
}

func printServerBannerCompact(output io.Writer, info ServerBannerInfo, startupDatetime string, useColors bool) {
	if useColors {
		fmt.Fprintf(output, "🚀 \x1b[1;36m%s\x1b[0m v%s\n", info.AppName, info.Version)
	} else {
		fmt.Fprintf(output, "🚀 %s v%s\n", info.AppName, info.Version)
	}
	printServerBannerAppModeLine(output, info.AppMode, true, info.Lang)
	printServerBannerOverlays(output, info, true)
	for _, address := range info.URLs {
		fmt.Fprintf(output, "🌐 %s: %s\n", i18n.T(info.Lang, "cli.listening_on_label"), address)
	}
	fmt.Fprintf(output, "✅ %s %s\n", i18n.T(info.Lang, "cli.server_started_on_label"), startupDatetime)
}

func printServerBannerOverlays(output io.Writer, info ServerBannerInfo, useIcons bool) {
	icon := func(value string) string {
		if useIcons {
			return value + " "
		}
		return ""
	}
	if info.OnionAddress != "" {
		fmt.Fprintf(output, "%sTor: %s\n", icon("🧅"), info.OnionAddress)
	}
	if info.I2PAddress != "" {
		fmt.Fprintf(output, "%sI2P: %s\n", icon("🔗"), info.I2PAddress)
	}
	if info.SMTPAddress != "" {
		fmt.Fprintf(output, "%sSMTP: %s\n", icon("📧"), info.SMTPAddress)
	}
	if info.SetupToken != "" {
		fmt.Fprintf(output, "%sSetup token: %s\n", icon("🔑"), info.SetupToken)
	}
}

func FormatStartupDatetime(startedAt time.Time, timezone string) string {
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "America/New_York"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		location, err = time.LoadLocation("America/New_York")
		if err != nil {
			location = time.UTC
		}
	}
	return startedAt.In(location).Format("January 02, 2006 at 15:04:05 MST")
}

func printServerBannerAppModeLine(output io.Writer, appMode string, useIcons bool, lang string) {
	icon := ""
	if useIcons {
		switch appMode {
		case "development":
			icon = "🔧 "
		case "debug":
			icon = "🐛 "
		default:
			icon = "🔒 "
		}
	}
	fmt.Fprintf(output, "%s%s: %s\n", icon, i18n.T(lang, "cli.running_in_mode_label"), appMode)
}

func extractHostPort(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Host != "" {
		host := parsed.Hostname()
		if port := parsed.Port(); port != "" {
			return net.JoinHostPort(host, port)
		}
		return host
	}
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if number, parseErr := strconv.Atoi(port); parseErr == nil && number > 0 {
			return net.JoinHostPort(host, port)
		}
	}
	return strings.TrimSpace(raw)
}
