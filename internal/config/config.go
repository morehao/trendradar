// Package config loads and validates trendradar's config.yaml. It never
// touches the network and never writes files, so it is safe to call at startup.
package config

import (
	"errors"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ErrInvalid marks any failure that should surface as IPC code CONFIG_INVALID.
var ErrInvalid = errors.New("config invalid")

// Config is the full trendradar configuration tree.
type Config struct {
	Model    ModelConfig    `yaml:"model"`
	Repo     RepoConfig     `yaml:"repo"`
	PR       PRConfig       `yaml:"pr"`
	Trending TrendingConfig `yaml:"trending"`
	Output   OutputConfig   `yaml:"output"`
	Schedule ScheduleConfig `yaml:"schedule"`
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
	return nil
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
}
