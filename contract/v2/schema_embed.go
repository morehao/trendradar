package contract

import "embed"

// SchemaFS embeds the canonical JSON Schema and OpenAPI files so consumers can
// validate payloads against the exact schemas this package was generated from.
//
//go:embed schema
var SchemaFS embed.FS
