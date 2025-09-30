package config

import (
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

// GitHub Bot Setup Instructions:
//
// To ensure PRs are created by a bot user instead of a personal account:
//
// Option 1: Bot User Account
// 1. Create a dedicated GitHub user account (e.g., "my-org-ponos-bot")
// 2. Generate a Personal Access Token for this bot user
// 3. Set GITHUB_TOKEN to the bot's personal access token
// 4. Set GITHUB_BOT_NAME to your preferred bot display name
//
// Option 2: GitHub App
// 1. Create a GitHub App in your organization
// 2. Install the app and get an installation token
// 3. Set GITHUB_TOKEN to the GitHub App installation token
// 4. Set GITHUB_APP_ID to your GitHub App ID
// 5. Set GITHUB_BOT_NAME to your GitHub App name
//
// Required permissions for the token/app:
// - Repository: Contents (read/write)
// - Repository: Pull requests (write)
// - Repository: Issues (write) - for PR descriptions

type Config struct {
	GitHubToken     string `envconfig:"GITHUB_TOKEN" default:""`        // Legacy: Personal access token
	GitHubAppID     string `envconfig:"GITHUB_APP_ID" default:""`       // GitHub App ID
	GitHubInstallID string `envconfig:"GITHUB_INSTALL_ID" default:""`   // GitHub App Installation ID  
	GitHubPEMKey    string `envconfig:"GITHUB_PEM_KEY" default:""`      // GitHub App Private Key (PEM)
	GitHubBotName   string `envconfig:"GITHUB_BOT_NAME" default:"ponos-bot"` // Bot display name

	SlackToken      string `envconfig:"SLACK_TOKEN" default:""`
	SlackSigningKey string `envconfig:"SLACK_SIGNING_SECRET" default:""`

	Port string `envconfig:"PORT" default:"8080"`
}

type ProjectConfig struct {
	Version  int       `yaml:"version"`
	Projects []Project `yaml:"projects"`
}

type Project struct {
	Network string   `yaml:"network"`
	Owner   string   `yaml:"owner"`
	Name    string   `yaml:"name"`
	Branch  string   `yaml:"branch"`
	Paths   []string `yaml:"paths"`
}

func Load() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

// ValidateGitHubBotConfig validates that the GitHub bot is properly configured
func (c *Config) ValidateGitHubBotConfig() error {
	// Check if GitHub App credentials are provided
	if c.GitHubAppID != "" || c.GitHubInstallID != "" || c.GitHubPEMKey != "" {
		if c.GitHubAppID == "" {
			return fmt.Errorf("GITHUB_APP_ID is required when using GitHub App authentication")
		}
		if c.GitHubInstallID == "" {
			return fmt.Errorf("GITHUB_INSTALL_ID is required when using GitHub App authentication") 
		}
		if c.GitHubPEMKey == "" {
			return fmt.Errorf("GITHUB_PEM_KEY is required when using GitHub App authentication")
		}
		return nil
	}
	
	// Fall back to token-based authentication
	if c.GitHubToken == "" {
		return fmt.Errorf("either GitHub App credentials (GITHUB_APP_ID, GITHUB_INSTALL_ID, GITHUB_PEM_KEY) or GITHUB_TOKEN is required")
	}
	
	return nil
}

func LoadProjectConfig(configPath string) (*ProjectConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var projectCfg ProjectConfig
	err = yaml.Unmarshal(data, &projectCfg)
	if err != nil {
		return nil, err
	}

	return &projectCfg, nil
}
