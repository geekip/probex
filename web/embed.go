package web

import "embed"

// Assets contains the production WebUI build.
//
//go:embed dist
var Assets embed.FS
