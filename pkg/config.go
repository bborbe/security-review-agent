// Copyright (c) 2025 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pkg

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bborbe/errors"
	"github.com/golang/glog"
	"gopkg.in/yaml.v3"
)

// GitHubConfig holds GitHub-specific configuration.
type GitHubConfig struct {
	Token string `yaml:"token"`
}

// BitbucketConfig holds Bitbucket-specific configuration.
type BitbucketConfig struct {
	Token    string `yaml:"token"`
	Username string `yaml:"username"`
}

// DefaultModel is the default Claude model to use when not specified in config.
const DefaultModel = "sonnet"

// DefaultGitHubToken is the default env var name for the GitHub token.
// #nosec G101 -- not a credential, just an env var name
const DefaultGitHubToken = "PR_REVIEWER_GITHUB_TOKEN"

// DefaultBitbucketToken is the default env var name for the Bitbucket token.
// #nosec G101 -- not a credential, just an env var name
const DefaultBitbucketToken = "BITBUCKET_TOKEN"

// DefaultContainerImage is the default Docker image for sandboxed reviews.
const DefaultContainerImage = "docker.io/bborbe/claude-yolo:v0.2.0"

// Config holds the pr-reviewer configuration.
type Config struct {
	GitHub         GitHubConfig    `yaml:"github"`
	Bitbucket      BitbucketConfig `yaml:"bitbucket"`
	Model          string          `yaml:"model"`
	ContainerImage string          `yaml:"containerImage"`
	AutoApprove    bool            `yaml:"autoApprove"`
	Repos          []RepoConfig    `yaml:"repos"`
}

// RepoConfig maps a repository URL to a local path.
type RepoConfig struct {
	URL           string `yaml:"url"`
	Path          string `yaml:"path"`
	ReviewCommand string `yaml:"reviewCommand"`
}

// RepoInfo holds repository information including path and review command.
type RepoInfo struct {
	Path          string
	ReviewCommand string
}

// envVarRefRegexp matches ${VAR_NAME} environment variable references.
var envVarRefRegexp = regexp.MustCompile(`^\$\{([A-Z_][A-Z0-9_]*)\}$`)

// resolveEnvVar resolves environment variable references in the format ${VAR_NAME}.
// If the value matches this pattern, it returns the value of the env var.
// Otherwise, it returns the value as-is.
func resolveEnvVar(value string) string {
	matches := envVarRefRegexp.FindStringSubmatch(value)
	if len(matches) == 2 {
		return os.Getenv(matches[1])
	}
	return value
}

// ResolvedGitHubToken returns the GitHub token with environment variable resolution.
// If no token is configured, falls back to DefaultGitHubToken env var.
func (c *Config) ResolvedGitHubToken() string {
	if c.GitHub.Token != "" {
		return resolveEnvVar(c.GitHub.Token)
	}
	return os.Getenv(DefaultGitHubToken)
}

// ResolvedBitbucketToken returns the Bitbucket token with environment variable resolution.
// If no token is configured, falls back to DefaultBitbucketToken env var.
func (c *Config) ResolvedBitbucketToken() string {
	if c.Bitbucket.Token != "" {
		return resolveEnvVar(c.Bitbucket.Token)
	}
	return os.Getenv(DefaultBitbucketToken)
}

// ResolvedModel returns the configured model if non-empty, otherwise returns DefaultModel.
func (c *Config) ResolvedModel() string {
	if c.Model != "" {
		return c.Model
	}
	return DefaultModel
}

// ResolvedContainerImage returns the configured container image if non-empty,
// otherwise returns DefaultContainerImage.
func (c *Config) ResolvedContainerImage() string {
	if c.ContainerImage != "" {
		return c.ContainerImage
	}
	return DefaultContainerImage
}

// Loader loads configuration from a source.
//
//counterfeiter:generate -o ../mocks/config-loader.go --fake-name ConfigLoader . Loader
type Loader interface {
	Load(ctx context.Context) (*Config, error)
}

// NewFileLoader creates a Loader that reads from a YAML file.
func NewFileLoader(configPath string) Loader {
	return &fileLoader{configPath: configPath}
}

type fileLoader struct {
	configPath string
}

func (l *fileLoader) Load(ctx context.Context) (*Config, error) {
	expandedPath := ExpandHome(l.configPath)

	// #nosec G304 -- path from constructor, typically ~/.config/maintainer/pr-reviewer.yaml
	data, err := os.ReadFile(expandedPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, errors.Wrapf(ctx, err, "config not found: %s", expandedPath)
		}
		return nil, errors.Wrapf(ctx, err, "read config file failed")
	}

	// Check file permissions - warn if world-readable
	fileInfo, err := os.Stat(expandedPath)
	if err == nil {
		mode := fileInfo.Mode()
		if mode&0004 != 0 {
			glog.Warningf(
				"config file is world-readable, consider: chmod 600 %s",
				expandedPath,
			)
		}
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, errors.Wrapf(ctx, err, "parse config failed")
	}

	if err := validateConfig(ctx, &cfg); err != nil {
		return nil, errors.Wrapf(ctx, err, "validate config failed")
	}

	return &cfg, nil
}

// ExpandHome expands ~ to the user's home directory.
func ExpandHome(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	return filepath.Join(home, path[1:])
}

func validateConfig(ctx context.Context, cfg *Config) error {
	for _, repo := range cfg.Repos {
		if repo.URL == "" || repo.Path == "" {
			return errors.Errorf(ctx, "invalid repo entry: url and path required")
		}
	}
	return nil
}

// FindRepo looks up the repository information including path and review command.
// Returns RepoInfo with path and reviewCommand. If reviewCommand is not specified,
// it remains empty and main.go will construct "/pr-review <target-branch>" dynamically.
func (c *Config) FindRepo(ctx context.Context, repoURL string) (*RepoInfo, error) {
	normalizedURL := normalizeURL(repoURL)

	for _, repo := range c.Repos {
		if normalizeURL(repo.URL) == normalizedURL {
			return &RepoInfo{
				Path:          repo.Path,
				ReviewCommand: repo.ReviewCommand,
			}, nil
		}
	}

	return nil, errors.Errorf(
		ctx,
		"repo not found in config, add to ~/.config/maintainer/pr-reviewer.yaml: %s",
		repoURL,
	)
}

func normalizeURL(url string) string {
	normalized := strings.ToLower(strings.TrimSpace(url))
	return strings.TrimSuffix(normalized, "/")
}
