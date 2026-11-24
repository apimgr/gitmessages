package template

import "embed"

// EmailFiles contains the built-in operator notification templates.
//
//go:embed email/*.txt
var EmailFiles embed.FS
