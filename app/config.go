package app

import (
	"fmt"
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
		ID      string `json:"id"`
		Message string `json:"message"`
		Title   string `json:"title"`
		URL     string `json:"url"`
		Author  struct {
			Name string `json:"name"`
		} `json:"author"`
	} `json:"commits"`
	TotalCommitsCount int `json:"total_commits_count"`
}

func (p GitLabPushEvent) SHA() string {
	if p.CheckoutSHA != "" {
		return p.CheckoutSHA
	}
	return p.After
}

func (p GitLabPushEvent) Author() string {
	if p.UserName != "" {
		return p.UserName
	}
	if p.UserLogin != "" {
		return p.UserLogin
	}
	if len(p.Commits) > 0 {
		return p.Commits[len(p.Commits)-1].Author.Name
	}
	return "Unknown"
}

// Алиас для совместимости
func (p GitLabPushEvent) AuthorName() string {
	return p.Author()
}

func (p GitLabPushEvent) Branch() string {
	// refs/heads/main -> main
	if strings.HasPrefix(p.Ref, "refs/heads/") {
		return strings.TrimPrefix(p.Ref, "refs/heads/")
	}
	return p.Ref
}

func (p GitLabPushEvent) CommitURL() string {
	// Берём URL последнего коммита (который задеплоили)
	if len(p.Commits) > 0 {
		return p.Commits[len(p.Commits)-1].URL
	}
	// Fallback: собираем URL из project web_url
	if p.Project.WebURL != "" && p.SHA() != "" {
		return fmt.Sprintf("%s/-/commit/%s", p.Project.WebURL, p.SHA())
	}
	return ""
}

func (p GitLabPushEvent) ProjectURL() string {
	return p.Project.WebURL
}

// CommitsSummary возвращает красивую сводку коммитов для quote
func (p GitLabPushEvent) CommitsSummary(maxCommits int) string {
	if len(p.Commits) == 0 {
		return ""
	}

	var lines []string

	// Показываем максимум N коммитов
	commits := p.Commits
	if len(commits) > maxCommits {
		commits = commits[len(commits)-maxCommits:] // последние N
	}

	for _, c := range commits {
		// Используем title (первая строка) вместо полного message
		title := c.Title
		if title == "" {
			title = strings.Split(c.Message, "\n")[0]
		}

		// Обрезаем длинные сообщения
		if len(title) > 60 {
			title = title[:57] + "..."
		}

		lines = append(lines, fmt.Sprintf("• %s", title))
	}

	// Если коммитов больше, чем показали
	if p.TotalCommitsCount > maxCommits {
		lines = append(lines, fmt.Sprintf("...and %d more commits", p.TotalCommitsCount-maxCommits))
	}

	return strings.Join(lines, "\n")
}
