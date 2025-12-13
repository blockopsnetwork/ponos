package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

type Config struct {
	GitHubToken     string `envconfig:"GITHUB_TOKEN" default:""`
	GitHubAppID     string `envconfig:"GITHUB_APP_ID" default:""`
	GitHubInstallID string `envconfig:"GITHUB_INSTALL_ID" default:""`
	GitHubPEMKey    string `envconfig:"GITHUB_PEM_KEY" default:""`
	GitHubBotName   string `envconfig:"GITHUB_BOT_NAME" default:"ponos-bot"`
	GitHubMCPURL    string `envconfig:"GITHUB_MCP_URL" default:"http://localhost:3001"`

	SlackToken      string `envconfig:"SLACK_TOKEN" default:""`
	SlackSigningKey string `envconfig:"SLACK_SIGNING_SECRET" default:""`
	SlackVerifyTok  string `envconfig:"SLACK_VERIFICATION_TOKEN" default:""`
	SlackChannel    string `envconfig:"SLACK_CHANNEL" default:"sre-tasks"`

	AgentCoreURL string `envconfig:"AGENT_CORE_URL" default:"http://localhost:8001"`

	Port string `envconfig:"PORT" default:"8080"`

	EnableReleaseListener bool `envconfig:"ENABLE_RELEASE_LISTENER" default:"false"`
}

type ProjectConfig struct {
	Version  int       `yaml:"version" json:"version"`
	Projects []Project `yaml:"projects" json:"projects"`
}

type Project struct {
	Network     string   `yaml:"network" json:"network"`
	ProjectName string   `yaml:"project_name" json:"project_name"`
	Owner       string   `yaml:"owner" json:"owner"`
	Name        string   `yaml:"name" json:"name"`
	Branch      string   `yaml:"branch" json:"branch"`
	Paths       []string `yaml:"paths" json:"paths"`
}

func Load() (*Config, error) {
	var cfg Config
	err := envconfig.Process("", &cfg)
	if err != nil {
		return nil, err
	}
	cfg.Sanitize()
	return &cfg, nil
}

func (c *Config) Sanitize() {
	c.GitHubToken = strings.TrimSpace(c.GitHubToken)
	c.GitHubAppID = strings.TrimSpace(c.GitHubAppID)
	c.GitHubInstallID = strings.TrimSpace(c.GitHubInstallID)
	c.GitHubPEMKey = strings.TrimSpace(c.GitHubPEMKey)
	c.GitHubBotName = strings.TrimSpace(c.GitHubBotName)
	c.GitHubMCPURL = strings.TrimSpace(c.GitHubMCPURL)

	c.SlackToken = strings.TrimSpace(c.SlackToken)
	c.SlackSigningKey = strings.TrimSpace(c.SlackSigningKey)
	c.SlackChannel = strings.TrimSpace(c.SlackChannel)
	c.SlackVerifyTok = strings.TrimSpace(c.SlackVerifyTok)

	c.AgentCoreURL = strings.TrimSpace(c.AgentCoreURL)
	c.Port = strings.TrimSpace(c.Port)
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
