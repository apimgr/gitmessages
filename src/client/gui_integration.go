package main

import clientgui "github.com/apimgr/gitmessages/src/client/gui"

func guiSupported() bool { return clientgui.IsGUIAvailable() }

func runGUI(server, language string, config *clientConfig) error {
	return clientgui.LaunchGUI(&clientgui.Config{
		Server:             server,
		Language:           language,
		ValidateServer:     validServer,
		DiscoverAPIVersion: discoverAPIVersion,
		Request:            request,
		SaveProfile: func(server, version string) error {
			config.Server.Primary = server
			config.Server.APIVersion = version
			return saveConfig(config)
		},
		LocalizeError: func(err error) string { return localizeClientError(language, err) },
	})
}
