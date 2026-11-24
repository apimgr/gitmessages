//go:build gui && !freebsd && !netbsd && !openbsd

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/messages"
	_ "github.com/gogpu/gg/gpu"
	"github.com/gogpu/gogpu"
	"github.com/gogpu/ui/app"
	"github.com/gogpu/ui/core/button"
	"github.com/gogpu/ui/core/textfield"
	"github.com/gogpu/ui/desktop"
	"github.com/gogpu/ui/primitives"
	"github.com/gogpu/ui/state"
	"github.com/gogpu/ui/theme/material3"
	"github.com/gogpu/ui/widget"
)

func guiSupported() bool { return true }

func runGUI(server, language string, config *clientConfig) error {
	output := state.NewSignal(i18n.T(language, "gui.description"))
	serverInput := state.NewSignal(server)
	kindInput := state.NewSignal("")
	scopeInput := state.NewSignal("")
	descriptionInput := state.NewSignal("")
	status := func(message string) { output.Set(message) }

	connect := func() {
		candidate := strings.TrimSpace(serverInput.Get())
		if !validServer(candidate) {
			status(i18n.T(language, "gui.invalid_server"))
			return
		}
		apiVersion, err := discoverAPIVersion(candidate, language)
		if err != nil {
			status(i18n.Format(language, "gui.connection_failed", map[string]string{"error": err.Error()}))
			return
		}
		if _, err := request(candidate, "GET", "/api/{api_version}/server/healthz", nil, language); err != nil {
			status(i18n.Format(language, "gui.connection_failed", map[string]string{"error": err.Error()}))
			return
		}
		config.Server.Primary = candidate
		config.Server.APIVersion = apiVersion
		if err := saveConfig(config); err != nil {
			status(i18n.Format(language, "gui.profile_save_failed", map[string]string{"error": err.Error()}))
			return
		}
		server = candidate
		status(i18n.T(language, "gui.connected"))
	}
	fetch := func(path, action string) {
		if !validServer(server) {
			status(i18n.T(language, "gui.connect_first"))
			return
		}
		data, err := request(server, "GET", path, nil, language)
		if err != nil {
			status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
			return
		}
		if action == "random" {
			var result struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
				return
			}
			status(result.Message)
			return
		}
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, data, "", "  "); err != nil {
			status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
			return
		}
		status(formatted.String())
	}
	build := func() {
		input := messages.BuildInput{Type: strings.TrimSpace(kindInput.Get()), Scope: strings.TrimSpace(scopeInput.Get()), Description: strings.TrimSpace(descriptionInput.Get()), Convention: "conventional-commits"}
		if _, err := messages.Build(input); err != nil {
			status(localizeClientError(language, err))
			return
		}
		payload, err := json.Marshal(input)
		if err != nil {
			status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
			return
		}
		data, err := request(server, "POST", "/api/{api_version}/messages", payload, language)
		if err != nil {
			status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
			return
		}
		var result struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			status(i18n.Format(language, "gui.response_error", map[string]string{"error": err.Error()}))
			return
		}
		status(result.Message)
	}

	primary := material3.New(widget.Hex(0x3659a2))
	root := primitives.VBox(
		primitives.Text(i18n.T(language, "home.title")).FontSize(26).Bold(),
		primitives.Text(i18n.T(language, "gui.description")).FontSize(14),
		primitives.HBox(
			textfield.New(textfield.Placeholder("https://server.example"), textfield.ValueSignal(serverInput)),
			button.New(button.TextOpt(i18n.T(language, "gui.connect")), button.OnClick(connect), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.HBox(
			button.New(button.TextOpt(i18n.T(language, "gui.types")), button.OnClick(func() { fetch("/api/{api_version}/types", "types") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt(i18n.T(language, "gui.conventions")), button.OnClick(func() { fetch("/api/{api_version}/conventions", "conventions") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt(i18n.T(language, "gui.templates")), button.OnClick(func() { fetch("/api/{api_version}/templates", "templates") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt(i18n.T(language, "gui.random")), button.OnClick(func() { fetch("/api/{api_version}/messages/random", "random") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.Text(i18n.T(language, "builder.title")).FontSize(18).Bold(),
		primitives.HBox(
			textfield.New(textfield.Placeholder(i18n.T(language, "builder.type")), textfield.ValueSignal(kindInput)),
			textfield.New(textfield.Placeholder(i18n.T(language, "builder.scope")), textfield.ValueSignal(scopeInput)),
			textfield.New(textfield.Placeholder(i18n.T(language, "builder.description")), textfield.ValueSignal(descriptionInput)),
			button.New(button.TextOpt(i18n.T(language, "builder.submit")), button.OnClick(build), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.TextFn(func() string { return output.Get() }).FontSize(14),
	).Padding(24).Gap(14).Background(widget.RGBA8(250, 250, 252, 255))

	gpuApp := gogpu.NewApp(gogpu.DefaultConfig().WithTitle(i18n.T(language, "home.title")).WithSize(900, 600))
	uiApp := app.New(app.WithWindowProvider(gpuApp), app.WithPlatformProvider(gpuApp), app.WithEventSource(gpuApp.EventSource()), app.WithTheme(primary.AsTheme()))
	uiApp.SetRoot(root)
	if err := desktop.Run(gpuApp, uiApp); err != nil {
		return fmt.Errorf("run Git Messages desktop interface: %w", err)
	}
	return nil
}
