// Package ruleset embeds the hand-written rule files so the WASM build and
// the CLI always ship the same rules.
package ruleset

import _ "embed"

// Boverket2026 is boverket-2026.json.
//
//go:embed boverket-2026.json
var Boverket2026 []byte
