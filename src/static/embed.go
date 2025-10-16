package static

import "embed"

//go:embed css js manifest.json images
var Content embed.FS
