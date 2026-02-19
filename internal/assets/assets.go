package assets

import "embed"

// WorkspaceTemplate contains the default workspace files.
//
//go:embed workspace/*
var WorkspaceTemplate embed.FS
