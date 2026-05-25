package app

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// NtfyNotifier implements Notifier using the ntfy protocol.
// Uses stdlib net/http only — no extra dependencies.
type NtfyNotifier struct {
	cfg NtfyConfig
}

// NewNtfyNotifier returns nil when ntfy is disabled or URL is empty.
func NewNtfyNotifier(cfg NtfyConfig) Notifier {
	if !cfg.Enabled || cfg.URL == "" {
		return nil
	}
	return &NtfyNotifier{cfg: cfg}
}

func (n *NtfyNotifier) Start(info DeployInfo) DeployHandle {
	n.send(
		fmt.Sprintf("🚀 Deploy started: %s", info.ProjectName),
		fmt.Sprintf("Branch: %s\nSHA: %s\nAuthor: %s", info.Branch, shortSHA(info.SHA), info.Author),
		n.prio(3),
	)
	return &ntfyHandle{n: n, info: info}
}

type ntfyHandle struct {
	n    *NtfyNotifier
	info DeployInfo
}

// Progress only fires on step failure to avoid notification spam.
func (h *ntfyHandle) Progress(idx int, status stepStatus) {
	if status != stepFailed {
		return
	}
	step := ""
	if idx < len(h.info.Steps) {
		step = h.info.Steps[idx].Name()
	}
	h.n.send(
		fmt.Sprintf("⚠️ Step failed: %s", h.info.ProjectName),
		fmt.Sprintf("Step: %s\nBranch: %s", step, h.info.Branch),
		h.n.prio(4),
	)
}

func (h *ntfyHandle) Success(info DeployInfo) {
	body := fmt.Sprintf("Branch: %s\nSHA: %s\nDuration: %s",
		info.Branch, shortSHA(info.SHA), info.Duration.Round(time.Second))

	if info.CommitsSummary != "" {
		body += "\n\nCommits:\n" + info.CommitsSummary
	}

	h.n.send(
		fmt.Sprintf("✅ Deploy successful: %s", info.ProjectName),
		body,
		h.n.prio(3),
	)
}

func (h *ntfyHandle) Fail(info DeployInfo) {
	body := fmt.Sprintf("Branch: %s\nSHA: %s", info.Branch, shortSHA(info.SHA))
	if info.Err != nil {
		body += "\nError: " + truncate(info.Err.Error(), 300)
	}
	h.n.send(
		fmt.Sprintf("❌ Deploy failed: %s", info.ProjectName),
		body,
		h.n.prio(5),
	)
}

func (n *NtfyNotifier) prio(def int) string {
	p := n.cfg.Priority
	if p == 0 {
		p = def
	}
	return fmt.Sprintf("%d", p)
}

func (n *NtfyNotifier) send(title, body, priority string) {
	req, err := http.NewRequest(http.MethodPost, n.cfg.URL, strings.NewReader(body))
	if err != nil {
		log.Printf("ntfy request error: %v", err)
		return
	}
	req.Header.Set("Title", title)
	req.Header.Set("Priority", priority)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if n.cfg.Token != "" {
		req.Header.Set("Authorization", "Bearer "+n.cfg.Token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("ntfy send error: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		log.Printf("ntfy: unexpected status %d for %q", resp.StatusCode, title)
	}
}
