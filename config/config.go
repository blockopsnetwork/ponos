package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	APIEndpoint  string             `envconfig:"AGENT_CORE_URL" default:"http://api.nodeoperator.ai" yaml:"api_endpoint"`
	APIKey       string             `envconfig:"API_KEY" default:"" yaml:"api_key"`
	Integrations IntegrationsConfig `yaml:"integrations"`
	Diagnostics  DiagnosticsConfig  `yaml:"diagnostics"`
	Server       ServerConfig       `yaml:"server"`
	Projects     []Project          `yaml:"projects"`
}

type IntegrationsConfig struct {
	GitHub    GitHubConfig    `yaml:"github"`
	Slack     SlackConfig     `yaml:"slack"`
	Telescope TelescopeConfig `yaml:"telescope"`
}

type TelescopeConfig struct {
	ProjectID          string `yaml:"project_id"`
	ProjectName        string `yaml:"project_name"`
	PrometheusURL      string `yaml:"prometheus_url"`
	PrometheusUsername string `yaml:"prometheus_username"`
	PrometheusPassword string `yaml:"prometheus_password"`
	LokiURL            string `yaml:"loki_url"`
	LokiUsername       string `yaml:"loki_username"`
	LokiPassword       string `yaml:"loki_password"`
}

type GitHubConfig struct {
	Token     string `envconfig:"GITHUB_TOKEN" default:"" yaml:"token"`
	AppID     string `envconfig:"GITHUB_APP_ID" default:"" yaml:"app_id"`
	InstallID string `envconfig:"GITHUB_INSTALL_ID" default:"" yaml:"install_id"`
	PEMKey    string `envconfig:"GITHUB_PEM_KEY" default:"" yaml:"pem_key"`
	BotName   string `envconfig:"GITHUB_BOT_NAME" default:"ponos-bot" yaml:"bot_name"`
	MCPURL    string `envconfig:"GITHUB_MCP_URL" default:"http://github-mcp.nodeoperator.ai" yaml:"mcp_url"`
}

type SlackConfig struct {
	Token       string `envconfig:"SLACK_TOKEN" default:"" yaml:"token"`
	SigningKey  string `envconfig:"SLACK_SIGNING_SECRET" default:"" yaml:"signing_key"`
	VerifyToken string `envconfig:"SLACK_VERIFICATION_TOKEN" default:"" yaml:"verify_token"`
	Channel     string `envconfig:"SLACK_CHANNEL" default:"sre-tasks" yaml:"channel"`
}

type DiagnosticsConfig struct {
	Enabled    bool                        `yaml:"enabled"`
	Provider   string                      `yaml:"provider"`
	GitHub     DiagnosticsGitHubConfig     `yaml:"github"`
	Slack      DiagnosticsSlackConfig      `yaml:"slack"`
	Kubernetes DiagnosticsKubernetesConfig `yaml:"kubernetes"`
	Monitoring DiagnosticsMonitoringConfig `yaml:"monitoring"`
}

type DiagnosticsSlackConfig struct {
	Channel string `yaml:"channel"`
}

type DiagnosticsGitHubConfig struct {
	Owner string `yaml:"owner"`
	Repo  string `yaml:"repo"`
}

type DiagnosticsKubernetesConfig struct {
	Namespace    string `yaml:"namespace"`
	ResourceType string `yaml:"resource_type"`
}

type DiagnosticsMonitoringConfig struct {
	Service      string `yaml:"service"`
	LogTail      int    `yaml:"log_tail"`
	EvalInterval int    `yaml:"eval_interval"`
}

type ServerConfig struct {
	Port                  string `envconfig:"PORT" default:"8080" yaml:"port"`
	EnableReleaseListener bool   `envconfig:"ENABLE_RELEASE_LISTENER" default:"false" yaml:"enable_release_listener"`
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
	if _, err := os.Stat("ponos.yml"); err != nil {
		return nil, fmt.Errorf("Ponos config (ponos.yml) missing, ensure you add the ponos.yml in the root directory")
	}
	
	data, err := os.ReadFile("ponos.yml")
	if err != nil {
		return nil, fmt.Errorf("failed to read ponos.yml: %w", err)
	}
	
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid ponos.yml format: %w", err)
	}
	
	cfg.Sanitize()
	return &cfg, nil
}

func (c *Config) Sanitize() {
	c.Integrations.GitHub.Token = strings.TrimSpace(c.Integrations.GitHub.Token)
	c.Integrations.GitHub.AppID = strings.TrimSpace(c.Integrations.GitHub.AppID)
	c.Integrations.GitHub.InstallID = strings.TrimSpace(c.Integrations.GitHub.InstallID)
	c.Integrations.GitHub.PEMKey = strings.TrimSpace(c.Integrations.GitHub.PEMKey)
	c.Integrations.GitHub.BotName = strings.TrimSpace(c.Integrations.GitHub.BotName)
	c.Integrations.GitHub.MCPURL = strings.TrimSpace(c.Integrations.GitHub.MCPURL)

	c.Integrations.Slack.Token = strings.TrimSpace(c.Integrations.Slack.Token)
	c.Integrations.Slack.SigningKey = strings.TrimSpace(c.Integrations.Slack.SigningKey)
	c.Integrations.Slack.Channel = strings.TrimSpace(c.Integrations.Slack.Channel)
	c.Integrations.Slack.VerifyToken = strings.TrimSpace(c.Integrations.Slack.VerifyToken)

	c.APIEndpoint = strings.TrimSpace(c.APIEndpoint)
	c.APIKey = strings.TrimSpace(c.APIKey)
	c.Server.Port = strings.TrimSpace(c.Server.Port)
}

func (c *Config) ValidateGitHubBotConfig() error {
	github := c.Integrations.GitHub
	if github.AppID != "" || github.InstallID != "" || github.PEMKey != "" {
		if github.AppID == "" {
			return fmt.Errorf("GITHUB_APP_ID is required when using GitHub App authentication")
		}
		if github.InstallID == "" {
			return fmt.Errorf("GITHUB_INSTALL_ID is required when using GitHub App authentication")
		}
		if github.PEMKey == "" {
			return fmt.Errorf("GITHUB_PEM_KEY is required when using GitHub App authentication")
		}
		return nil
	}

	if github.Token == "" {
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
