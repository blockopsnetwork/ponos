package config

import (
	"os"

	"github.com/kelseyhightower/envconfig"
	"gopkg.in/yaml.v3"
)

type Config struct {
	MySQLDSN string `envconfig:"MYSQL_DSN" default:"root:root@tcp(127.0.0.1:3306)/ponos?parseTime=true"`

	GitHubPEMKey    string `envconfig:"GITHUB_PEM_KEY" default:""`
	GitHubAppID     int64  `envconfig:"GITHUB_APP_ID" default:"0"`
	GitHubInstallID int64  `envconfig:"GITHUB_INSTALL_ID" default:"0"`

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
