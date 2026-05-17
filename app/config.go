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

func (d DeployStep) Name() string {
	if len(d.Args) == 0 {
		return d.Cmd
	}
	return fmt.Sprintf("%s %s", d.Cmd, strings.Join(d.Args, " "))
}

type ProjectConfig struct {
	Name         string       `yaml:"name"`
	WorkingDir   string       `yaml:"working_dir"`
	DeploySteps  []DeployStep `yaml:"deploy_steps"`
	DebounceTime string       `yaml:"debounce_time"`
	SSHKeyPath   string       `yaml:"ssh_key_path"`
	DeployBranch string       `yaml:"deploy_branch"`
}

func (p *ProjectConfig) GetDebounceDuration() time.Duration {
	if p.DebounceTime == "" {
		return 10 * time.Second
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

// NtfyConfig configures ntfy.sh push notifications (no extra dependencies, stdlib only).
type NtfyConfig struct {
	Enabled  bool   `yaml:"enabled"`
	URL      string `yaml:"url"`             // e.g. https://ntfy.sh/my-topic or self-hosted
	Token    string `yaml:"token"`           // optional Bearer token
	Priority int    `yaml:"priority"`        // 1–5, default 3; per-event overrides apply
}

// NotifyConfig controls which channels receive deploy events and how.
type NotifyConfig struct {
	// Mode: "all" sends to every channel, "fallback" stops at the first available one.
	Mode string `yaml:"mode"`
	// Channels lists notifiers in priority order. Supported: "telegram", "ntfy".
	// Defaults to ["telegram", "ntfy"] when omitted.
	Channels []string `yaml:"channels"`
}

type Config struct {
	Server struct {
		Port        string `yaml:"port"`
		StoragePath string `yaml:"storage_path"` // default: "deploys.jsonl"
		HistorySize int    `yaml:"history_size"`  // last N deploys per project, default: 3
	} `yaml:"server"`

	DeployHeaderKey string                   `yaml:"deploy_header_key"`
	Projects        map[string]ProjectConfig `yaml:"projects"`

	Telegram Telegram     `yaml:"telegram"`
	Ntfy     NtfyConfig   `yaml:"ntfy"`
	Notify   NotifyConfig `yaml:"notify"`
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
