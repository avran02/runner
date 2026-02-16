package app

import (
	"log"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type DeployStep struct {
	Cmd  string   `yaml:"cmd"`
	Args []string `yaml:"args"`
}

func (s DeployStep) Name() string {
	if s.Cmd == "" {
		return "unknow"
	}
	parts := append([]string{s.Cmd}, s.Args...)
	name := strings.Join(parts, " ")
	if len(name) > 40 {
		return name[:40] + "..."
	}
	return name
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

type Telegram struct {
	Enabled  bool   `yaml:"enabled"`
	BotToken string `yaml:"bot_token"`
	ChatID   int64  `yaml:"chat_id"`
	ThreadID int64  `yaml:"thread_id"`
}

type Config struct {
	Server struct {
		Port string `yaml:"port"`
	} `yaml:"server"`

	DeployHeaderKey string `yaml:"deploy_header_key"`

	// Map: gitlab secret token -> deploy config
	Projects map[string]ProjectConfig `yaml:"projects"`

	Telegram Telegram `yaml:"telegram"`
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
	UserName    string `json:"user_name"`
	UserLogin   string `json:"user_username"`
	Project     struct {
		WebURL string `json:"web_url"`
	} `json:"project"`
	Commits []struct {
		URL    string `json:"url"`
		Author struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
}

func (p GitLabPushEvent) ProjectURL() string {
	return p.Project.WebURL
}

func (p GitLabPushEvent) Branch() string {
	return strings.TrimPrefix(p.Ref, "refs/heads/")
}

func (p GitLabPushEvent) AuthorName() string {
	if len(p.Commits) > 0 && p.Commits[0].Author.Name != "" {
		return p.Commits[0].Author.Name
	}
	if p.UserName != "" {
		return p.UserName
	}
	return p.UserLogin
}

func (p GitLabPushEvent) CommitURL() string {
	if len(p.Commits) > 0 && p.Commits[0].URL != "" {
		return p.Commits[0].URL
	}
	return "#"
}

func (p GitLabPushEvent) SHA() string {
	if p.CheckoutSHA != "" {
		return p.CheckoutSHA
	}
	return p.After
}
