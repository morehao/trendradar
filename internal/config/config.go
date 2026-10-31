// Package config loads and validates trendradar's config.yaml. It never
// touches the network and never writes files, so it is safe to call at startup.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrInvalid marks any failure that should surface as IPC code CONFIG_INVALID.
var ErrInvalid = errors.New("config invalid")

// Config is the full trendradar configuration tree.
type Config struct {
	Model     ModelConfig     `yaml:"model"`
	Repo      RepoConfig      `yaml:"repo"`
	PR        PRConfig        `yaml:"pr"`
	Trending  TrendingConfig  `yaml:"trending"`
	Output    OutputConfig    `yaml:"output"`
	Schedule  ScheduleConfig  `yaml:"schedule"`
	Summarize SummarizeConfig `yaml:"summarize"`
	Releases  ReleasesConfig  `yaml:"releases"`
}

// ReleasesConfig controls the optional repo-release subscription stage. The
// subscription list itself lives in a separate subscriptions.yaml managed by
// `trendradar subscribe`; this struct only holds global behaviour knobs.
type ReleasesConfig struct {
	Enabled           bool   `yaml:"enabled"`
	SubscriptionsFile string `yaml:"subscriptionsFile"`
	IncludePrerelease bool   `yaml:"includePrerelease"`
	IncludeDrafts     bool   `yaml:"includeDrafts"`

	// Pointers so an absent key defaults to true while an explicit false is
	// still honoured (plain bool cannot tell "unset" from "false").
	FallbackToTags     *bool `yaml:"fallbackToTags"`
	BaselineOnFirstRun *bool `yaml:"baselineOnFirstRun"`

	BackfillLatest int `yaml:"backfillLatest" validate:"omitempty,gte=0"`
	// PerRepoLimit caps how many versions a document *lists*; it no longer caps
	// how many are fetched (the weekly summary covers every version).
	PerRepoLimit  int `yaml:"perRepoLimit" validate:"omitempty,gte=1,lte=20"`
	MaxNotesChars int `yaml:"maxNotesChars" validate:"omitempty,gte=500,lte=20000"`
	// SummaryMaxChars bounds the one-per-repo weekly summary (design D17).
	SummaryMaxChars int `yaml:"summaryMaxChars" validate:"omitempty,gte=500,lte=2000"`
	// DocMaxLines is the per-repo document's line budget. A new volume starts
	// only when the current one already exceeds it *before* a write.
	DocMaxLines    int     `yaml:"docMaxLines" validate:"omitempty,gte=100,lte=2000"`
	Timezone       string  `yaml:"timezone"`
	WeekStart      string  `yaml:"weekStart" validate:"omitempty,oneof=monday"`
	Concurrency    AutoInt `yaml:"concurrency"`
	TimeoutSeconds int     `yaml:"timeoutSeconds" validate:"omitempty,gte=1,lte=60"`
	BudgetSeconds  AutoInt `yaml:"budgetSeconds"`
	MaxPages       int     `yaml:"maxPages" validate:"omitempty,gte=1,lte=50"`
	OutputDir      string  `yaml:"outputDir"`
	// Period is the summary period. Only "weekly" is implemented; the releases
	// stage summarizes one ISO week per model call per repo.
	Period string `yaml:"period" validate:"omitempty,oneof=weekly"`
}

// AutoInt accepts either "auto" or an explicit integer in YAML. "auto" resolves
// per run from the subscription count (design D12); an explicit number wins.
type AutoInt struct {
	Auto  bool
	Value int
}

// UnmarshalYAML accepts `auto`, an integer, or a quoted integer. A plain int
// field could not express "auto" without breaking existing configs that spell
// the value out.
func (a *AutoInt) UnmarshalYAML(node *yaml.Node) error {
	raw := strings.TrimSpace(node.Value)
	if strings.EqualFold(raw, "auto") {
		a.Auto = true
		a.Value = 0
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("want \"auto\" or an integer, got %q", node.Value)
	}
	a.Auto = false
	a.Value = n
	return nil
}

// MarshalYAML round-trips so `config` output and generated YAML stay readable.
func (a AutoInt) MarshalYAML() (any, error) {
	if a.Auto {
		return "auto", nil
	}
	return a.Value, nil
}

// Resolve returns the effective value: clamp(min, ceil(n/10), max) when auto.
func (a AutoInt) Resolve(n, min, max int) int {
	if !a.Auto {
		return a.Value
	}
	v := (n + 9) / 10
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return v
}

// ResolveBudget returns min(max, 30+3n) when auto, else the explicit value.
func (a AutoInt) ResolveBudget(n, max int) int {
	if !a.Auto {
		return a.Value
	}
	v := 30 + 3*n
	if v > max {
		v = max
	}
	return v
}

// FallbackToTagsEnabled reports the effective fallbackToTags (default true).
func (r ReleasesConfig) FallbackToTagsEnabled() bool {
	if r.FallbackToTags == nil {
		return true
	}
	return *r.FallbackToTags
}

// IsZero reports whether the knob was left unset (so defaults can apply).
func (a AutoInt) IsZero() bool { return !a.Auto && a.Value == 0 }

// ValidateAuto reports range problems for the auto-or-number knobs, which the
// struct-tag validator cannot express.
func (r ReleasesConfig) ValidateAuto() error {
	// A zero value means "unset": Load fills it with auto, and a struct built
	// directly in tests is allowed to leave it alone. Only an out-of-range
	// explicit number is a caller error.
	if !r.Concurrency.Auto && r.Concurrency.Value != 0 && (r.Concurrency.Value < 1 || r.Concurrency.Value > 8) {
		return fmt.Errorf("releases.concurrency: want auto or 1-8, got %d", r.Concurrency.Value)
	}
	if !r.BudgetSeconds.Auto && r.BudgetSeconds.Value != 0 && (r.BudgetSeconds.Value < 1 || r.BudgetSeconds.Value > 300) {
		return fmt.Errorf("releases.budgetSeconds: want auto or 1-300, got %d", r.BudgetSeconds.Value)
	}
	return nil
}

// BaselineOnFirstRunEnabled reports the effective baselineOnFirstRun (default true).
func (r ReleasesConfig) BaselineOnFirstRunEnabled() bool {
	if r.BaselineOnFirstRun == nil {
		return true
	}
	return *r.BaselineOnFirstRun
}

// SummarizeConfig controls summary generation and the optional external-context
// (enrich) stage. Enrich defaults to "none" so a run never touches GitHub extra
// endpoints unless an operator opts in.
type SummarizeConfig struct {
	BatchSize  int `yaml:"batchSize" validate:"omitempty,gt=0"`
	MaxRetries int `yaml:"maxRetries" validate:"omitempty,gte=0"`

	// Enrich selects external context: none (default) | repo | readme.
	Enrich string `yaml:"enrich" validate:"omitempty,oneof=none repo readme"`
	// ReadmeChars is the README excerpt length in runes (default 800).
	ReadmeChars int `yaml:"readmeChars" validate:"omitempty,gt=0"`
	// EnrichCacheTTLHours is how long fetched topics/README stays cached.
	EnrichCacheTTLHours int `yaml:"enrichCacheTTLHours" validate:"omitempty,gt=0"`
	// EnrichConcurrency caps concurrent per-repo fetches.
	EnrichConcurrency int `yaml:"enrichConcurrency" validate:"omitempty,gt=0"`
	// EnrichTimeoutSeconds is the per-request HTTP timeout.
	EnrichTimeoutSeconds int `yaml:"enrichTimeoutSeconds" validate:"omitempty,gt=0"`
	// EnrichBudgetSeconds is the whole-run enrich budget (readme defaults higher).
	EnrichBudgetSeconds int `yaml:"enrichBudgetSeconds" validate:"omitempty,gt=0"`
}

// ModelConfig maps onto golib/gllm's provider/model concept.
type ModelConfig struct {
	Provider      string   `yaml:"provider" validate:"required"`
	Name          string   `yaml:"name" validate:"required"`
	BaseURL       string   `yaml:"baseURL" validate:"required,url"`
	APIKey        string   `yaml:"apiKey"` // may be empty -> allowDegraded governs behaviour
	Temperature   *float32 `yaml:"temperature" validate:"omitempty,gte=0,lte=2"`
	MaxTokens     int      `yaml:"maxTokens" validate:"omitempty,gt=0"`
	AllowDegraded bool     `yaml:"allowDegraded"`
}

// RepoConfig points at the target git repository the core writes into.
type RepoConfig struct {
	Path        string `yaml:"path" validate:"required"`
	Remote      string `yaml:"remote"`
	BaseBranch  string `yaml:"baseBranch"`
	AuthorName  string `yaml:"authorName"`
	AuthorEmail string `yaml:"authorEmail" validate:"omitempty,email"`
}

// PRConfig controls pull-request creation and merge.
type PRConfig struct {
	Enabled       bool     `yaml:"enabled"`
	TitleTemplate string   `yaml:"titleTemplate"`
	Labels        []string `yaml:"labels"`
	AutoMerge     bool     `yaml:"autoMerge"`
}

// TrendingConfig controls what to fetch.
type TrendingConfig struct {
	Languages   []string `yaml:"languages" validate:"required,min=1,dive,required"`
	Since       string   `yaml:"since" validate:"required,oneof=daily weekly monthly"`
	PerLanguage int      `yaml:"perLanguage" validate:"omitempty,gt=0"`
}

// OutputConfig controls where rendered digests go.
type OutputConfig struct {
	Dir           string `yaml:"dir" validate:"required"`
	BranchPrefix  string `yaml:"branchPrefix"`
	CommitMessage string `yaml:"commitMessage"`
	RetentionDays int    `yaml:"retentionDays"`
}

// ScheduleConfig is optional; used only when running in serve mode.
type ScheduleConfig struct {
	Cron string `yaml:"cron"`
}

// Load reads, defaults and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrInvalid, path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("%w: parse yaml: %v", ErrInvalid, err)
	}
	c.expandEnv()
	c.withDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Validate applies the struct-tag rules. See validate.go for why this does not
// call golib/gvalidate directly.
func (c *Config) Validate() error {
	if err := validateStruct(c); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := c.Releases.ValidateAuto(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return nil
}

// expandEnv substitutes ${VAR} in string fields so secrets can live in a local
// .env file instead of committed config.yaml.
func (c *Config) expandEnv() {
	c.Model.Provider = os.ExpandEnv(c.Model.Provider)
	c.Model.Name = os.ExpandEnv(c.Model.Name)
	c.Model.BaseURL = os.ExpandEnv(c.Model.BaseURL)
	c.Model.APIKey = os.ExpandEnv(c.Model.APIKey)
	c.Repo.Path = os.ExpandEnv(c.Repo.Path)
	c.Repo.Remote = os.ExpandEnv(c.Repo.Remote)
	c.Repo.BaseBranch = os.ExpandEnv(c.Repo.BaseBranch)
	c.Repo.AuthorName = os.ExpandEnv(c.Repo.AuthorName)
	c.Repo.AuthorEmail = os.ExpandEnv(c.Repo.AuthorEmail)
	c.Output.Dir = os.ExpandEnv(c.Output.Dir)
	c.Output.BranchPrefix = os.ExpandEnv(c.Output.BranchPrefix)
	c.Releases.SubscriptionsFile = os.ExpandEnv(c.Releases.SubscriptionsFile)
	c.Releases.OutputDir = os.ExpandEnv(c.Releases.OutputDir)
}

func (c *Config) withDefaults() {
	if c.Repo.Remote == "" {
		c.Repo.Remote = "origin"
	}
	if c.Repo.BaseBranch == "" {
		c.Repo.BaseBranch = "main"
	}
	if c.Repo.AuthorName == "" {
		c.Repo.AuthorName = "trendradar"
	}
	if c.Trending.Since == "" {
		c.Trending.Since = "daily"
	}
	if c.Trending.PerLanguage == 0 {
		c.Trending.PerLanguage = 20
	}
	if c.Output.BranchPrefix == "" {
		c.Output.BranchPrefix = "trending/"
	}
	if c.Output.RetentionDays == 0 {
		c.Output.RetentionDays = 90
	}
	if c.Summarize.BatchSize == 0 {
		c.Summarize.BatchSize = 10
	}
	if c.Summarize.MaxRetries == 0 {
		c.Summarize.MaxRetries = 1
	}
	if c.Summarize.Enrich == "" {
		c.Summarize.Enrich = "none"
	}
	if c.Summarize.ReadmeChars == 0 {
		c.Summarize.ReadmeChars = 800
	}
	if c.Summarize.EnrichCacheTTLHours == 0 {
		c.Summarize.EnrichCacheTTLHours = 168
	}
	if c.Summarize.EnrichConcurrency == 0 {
		c.Summarize.EnrichConcurrency = 4
	}
	if c.Summarize.EnrichTimeoutSeconds == 0 {
		c.Summarize.EnrichTimeoutSeconds = 5
	}
	if c.Summarize.EnrichBudgetSeconds == 0 {
		if c.Summarize.Enrich == "readme" {
			c.Summarize.EnrichBudgetSeconds = 12
		} else {
			c.Summarize.EnrichBudgetSeconds = 8
		}
	}
	if c.Releases.SubscriptionsFile == "" {
		c.Releases.SubscriptionsFile = "subscriptions.yaml"
	}
	if c.Releases.PerRepoLimit == 0 {
		c.Releases.PerRepoLimit = 5
	}
	if c.Releases.MaxNotesChars == 0 {
		c.Releases.MaxNotesChars = 4000
	}
	// Auto is the default for both knobs: the plugin and a bare CLI run then get
	// the same self-tuning behaviour without spelling a number in config.
	if c.Releases.Concurrency.IsZero() {
		c.Releases.Concurrency = AutoInt{Auto: true}
	}
	if c.Releases.TimeoutSeconds == 0 {
		c.Releases.TimeoutSeconds = 20
	}
	if c.Releases.BudgetSeconds.IsZero() {
		c.Releases.BudgetSeconds = AutoInt{Auto: true}
	}
	if c.Releases.MaxPages == 0 {
		c.Releases.MaxPages = 10
	}
	if c.Releases.OutputDir == "" {
		c.Releases.OutputDir = "releases"
	}
	if c.Releases.Period == "" {
		c.Releases.Period = "weekly"
	}
	if c.Releases.WeekStart == "" {
		c.Releases.WeekStart = "monday"
	}
	if c.Releases.Timezone == "" {
		c.Releases.Timezone = "Asia/Shanghai"
	}
	if c.Releases.DocMaxLines == 0 {
		c.Releases.DocMaxLines = 600
	}
	if c.Releases.SummaryMaxChars == 0 {
		c.Releases.SummaryMaxChars = 1200
	}
}
