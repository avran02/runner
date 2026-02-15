package app

import (
	"log"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type DeployStep struct {
	Cmd  string   `yaml:"cmd"`
	Args []string `yaml:"args"`
}

type ProjectConfig struct {
	Name         string       `yaml:"name"`
	WorkingDir   string       `yaml:"working_dir"`
	DeploySteps  []DeployStep `yaml:"deploy_steps"`
	DebounceTime string       `yaml:"debounce_time"` // например "30s"
	SSHKeyPath   string       `yaml:"ssh_key_path"`  // путь к приватному ключу для git
	DeployBranch string       `yaml:"deploy_branch"` // ветка для деплоя, по умолчанию "master"
}

func (p *ProjectConfig) GetDebounceDuration() time.Duration {
	if p.DebounceTime == "" {
		return 10 * time.Second // default
	}
	d, err := time.ParseDuration(p.DebounceTime)
	if err != nil {
		return 10 * time.Second
	}
	return d
}

func (p *ProjectConfig) GetDeployBranch() string {
	if p.DeployBranch == "" {
		return "master"
	}
	return p.DeployBranch
}

type Config struct {
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`

	DeployHeaderKey string `yaml:"deploy_header_key"`

	// Map: gitlab secret token -> deploy config
	Projects map[string]ProjectConfig `yaml:"projects"`

	Telegram struct {
		Enabled  bool   `yaml:"enabled"`
		BotToken string `yaml:"bot_token"`
		ChatID   int64  `yaml:"chat_id"`
		ThreadID int64  `yaml:"thread_id"`
	} `yaml:"telegram"`
}

func MustLoadConfig(path string) Config {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("config open error: %v", err)
	}
	defer f.Close()

	var cfg Config
	if err := yaml.NewDecoder(f).Decode(&cfg); err != nil {
		log.Fatalf("config decode error: %v", err)
	}

	return cfg
}

type GitLabPushEvent struct {
	Ref         string `json:"ref"`
	CheckoutSHA string `json:"checkout_sha"`
	After       string `json:"after"`
}

func (p GitLabPushEvent) SHA() string {
	if p.CheckoutSHA != "" {
		return p.CheckoutSHA
	}
	return p.After
}
