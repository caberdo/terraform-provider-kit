// Package scaffold embeds the canonical repository scaffold.
//
// The tree beside this file is the source of truth for the files every
// provider repository shares. Embedding it lets `tfkit scaffold check` compare
// a provider against it without a checkout of the kit, which is what a
// `go run ...@vX.Y.Z` invocation from a provider repository would otherwise
// need.
//
// This file is the embed declaration, not part of the scaffold: the renderer
// skips it. Everything the renderer returns is a file a provider repository
// should contain, with its placeholders substituted.
package scaffold

import "embed"

// FS holds the canonical tree, rooted at this directory. Paths are the
// repository-relative paths of the files, such as ".github/CODEOWNERS".
//
// The patterns are explicit rather than a single "all:." so that this
// declaration is the only file in the directory that is not scaffold content.
//
//go:embed .editorconfig .gitignore .golangci.yml GNUmakefile LICENSE NOTICE README.md AGENTS.md ARCHITECTURE.md CONTRIBUTING.md SECURITY.md CODE_OF_CONDUCT.md GOVERNANCE.md ROADMAP.md SUPPORT.md
//go:embed all:.github
var FS embed.FS
