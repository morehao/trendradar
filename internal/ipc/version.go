package ipc

import contract "github.com/morehao/trendradar/contract"

// DigestSchemaVersion is the version of the digest document produced by render.
const DigestSchemaVersion = 2

// ContractVersion is the version of the shared contract package this core targets.
const ContractVersion = "0.2.0"

// Build-time values. Override at release time with:
//
//	go build -ldflags "-X github.com/morehao/trendradar/internal/ipc.Version=$(git describe --tags) \
//	                    -X github.com/morehao/trendradar/internal/ipc.Commit=$(git rev-parse --short HEAD)"
var (
	Version = "0.3.0"
	Commit  = "dev"
)

// VersionInfo is the handshake payload returned by `trendradar version --json`.
type VersionInfo = contract.VersionInfo

// Current returns the build's VersionInfo.
func Current() VersionInfo {
	return VersionInfo{
		Version:            Version,
		Commit:             Commit,
		IPCSchema:          SchemaVersion,
		DigestSchema:       DigestSchemaVersion,
		ContractVersion:    ContractVersion,
		SupportedProviders: []string{"openai-compatible", "deepseek"},
	}
}
