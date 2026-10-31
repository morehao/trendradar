package app

import contract "github.com/morehao/trendradar/contract"

// Stage is one timed step of a run.
type Stage struct {
	Name       string `json:"name"`
	DurationMs int64  `json:"durationMs"`
	Degraded   bool   `json:"degraded,omitempty"`
	Retries    int    `json:"retries,omitempty"`
}

// Report summarizes a run's timing and warnings. Usage is the shared contract
// type so the core and the TypeScript plugin agree on the wire shape.
type Report struct {
	Stages   []Stage         `json:"stages"`
	Warnings []string        `json:"warnings,omitempty"`
	TotalMs  int64           `json:"totalMs"`
	Usage    *contract.Usage `json:"usage,omitempty"`
}
