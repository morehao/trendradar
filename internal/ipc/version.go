package ipc

// DigestSchemaVersion is the version of the digest document produced by render.
const DigestSchemaVersion = 1

// Build-time values. Override at release time with:
//
//	go build -ldflags "-X github.com/morehao/trendradar/internal/ipc.Version=$(git describe --tags) \
//	                    -X github.com/morehao/trendradar/internal/ipc.Commit=$(git rev-parse --short HEAD)"
var (
	Version = "0.1.0"
	Commit  = "dev"
)

// VersionInfo is the handshake payload returned by `trendradar version --json`.
// The plugin matches ipcSchema and digestSchema before trusting the core.
type VersionInfo struct {
	Version            string   `json:"version"`
	Commit             string   `json:"commit"`
	IPCSchema          int      `json:"ipcSchema"`
	DigestSchema       int      `json:"digestSchema"`
	SupportedProviders []string `json:"supportedProviders"`
}

// Current returns the build's VersionInfo.
func Current() VersionInfo {
	return VersionInfo{
		Version:            Version,
		Commit:             Commit,
		IPCSchema:          SchemaVersion,
		DigestSchema:       DigestSchemaVersion,
		SupportedProviders: []string{"openai-compatible", "deepseek"},
	}
}
