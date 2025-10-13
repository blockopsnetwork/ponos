package config

import (
	"fmt"
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

type Config struct {
	GitHubToken     string `envconfig:"GITHUB_TOKEN" default:""`
	GitHubAppID     string `envconfig:"GITHUB_APP_ID" default:""`
	GitHubInstallID string `envconfig:"GITHUB_INSTALL_ID" default:""`
	GitHubPEMKey    string `envconfig:"GITHUB_PEM_KEY" default:""`
	GitHubBotName   string `envconfig:"GITHUB_BOT_NAME" default:"ponos-bot"`

	SlackToken         string `envconfig:"SLACK_TOKEN" default:""`
	SlackSigningKey    string `envconfig:"SLACK_SIGNING_SECRET" default:""`
	SlackUpdateChannel string `envconfig:"SLACK_UPDATE_CHANNEL" default:"sre-tasks"`

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

func (c *Config) ValidateGitHubBotConfig() error {
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
