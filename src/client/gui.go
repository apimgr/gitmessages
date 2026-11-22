//go:build !freebsd && !netbsd && !openbsd

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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

func runGUI(server, language string, config *clientConfig) error {
	output := state.NewSignal("Choose an action to browse the public Git Messages API.")
	serverInput := state.NewSignal(server)
	kindInput := state.NewSignal("")
	scopeInput := state.NewSignal("")
	descriptionInput := state.NewSignal("")
	status := func(message string) { output.Set(message) }

	connect := func() {
		candidate := strings.TrimSpace(serverInput.Get())
		if !validServer(candidate) {
			status("Enter a valid http or https server URL.")
			return
		}
		if _, err := request(candidate, "GET", "/api/v1/server/healthz", nil, language); err != nil {
			status("Connection failed: " + err.Error())
			return
		}
		config.Server.Primary = candidate
		if err := saveConfig(config); err != nil {
			status(err.Error())
			return
		}
		server = candidate
		status("Connected and saved the server profile.")
	}
	fetch := func(path, action string) {
		if !validServer(server) {
			status("Connect to a server before requesting data.")
			return
		}
		data, err := request(server, "GET", path, nil, language)
		if err != nil {
			status(err.Error())
			return
		}
		if action == "random" {
			var result struct {
				Message string `json:"message"`
			}
			if err := json.Unmarshal(data, &result); err != nil {
				status(err.Error())
				return
			}
			status(result.Message)
			return
		}
		var formatted bytes.Buffer
		if err := json.Indent(&formatted, data, "", "  "); err != nil {
			status(err.Error())
			return
		}
		status(formatted.String())
	}
	build := func() {
		input := messages.BuildInput{Type: strings.TrimSpace(kindInput.Get()), Scope: strings.TrimSpace(scopeInput.Get()), Description: strings.TrimSpace(descriptionInput.Get()), Convention: "conventional-commits"}
		if _, err := messages.Build(input); err != nil {
			status(err.Error())
			return
		}
		payload, err := json.Marshal(input)
		if err != nil {
			status(err.Error())
			return
		}
		data, err := request(server, "POST", "/api/v1/messages", payload, language)
		if err != nil {
			status(err.Error())
			return
		}
		var result struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			status(err.Error())
			return
		}
		status(result.Message)
	}

	primary := material3.New(widget.Hex(0x3659a2))
	root := primitives.VBox(
		primitives.Text("Git Messages").FontSize(26).Bold(),
		primitives.Text("Browse commit conventions and build a message.").FontSize(14),
		primitives.HBox(
			textfield.New(textfield.Placeholder("https://server.example"), textfield.ValueSignal(serverInput)),
			button.New(button.TextOpt("Connect"), button.OnClick(connect), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.HBox(
			button.New(button.TextOpt("Types"), button.OnClick(func() { fetch("/api/v1/types", "types") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt("Conventions"), button.OnClick(func() { fetch("/api/v1/conventions", "conventions") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt("Templates"), button.OnClick(func() { fetch("/api/v1/templates", "templates") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
			button.New(button.TextOpt("Random"), button.OnClick(func() { fetch("/api/v1/messages/random", "random") }), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.Text("Build a commit message").FontSize(18).Bold(),
		primitives.HBox(
			textfield.New(textfield.Placeholder("Type"), textfield.ValueSignal(kindInput)),
			textfield.New(textfield.Placeholder("Scope (optional)"), textfield.ValueSignal(scopeInput)),
			textfield.New(textfield.Placeholder("Description"), textfield.ValueSignal(descriptionInput)),
			button.New(button.TextOpt("Build"), button.OnClick(build), button.PainterOpt(material3.ButtonPainter{Theme: primary})),
		).Gap(8),
		primitives.TextFn(func() string { return output.Get() }).FontSize(14),
	).Padding(24).Gap(14).Background(widget.RGBA8(250, 250, 252, 255))

	gpuApp := gogpu.NewApp(gogpu.DefaultConfig().WithTitle("Git Messages").WithSize(900, 600))
	uiApp := app.New(app.WithWindowProvider(gpuApp), app.WithPlatformProvider(gpuApp), app.WithEventSource(gpuApp.EventSource()), app.WithTheme(primary.AsTheme()))
	uiApp.SetRoot(root)
	if err := desktop.Run(gpuApp, uiApp); err != nil {
		return fmt.Errorf("run Git Messages desktop interface: %w", err)
	}
	return nil
}
