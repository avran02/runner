package app

import (
	"fmt"
	"strings"
)

// GitLabPushEvent is the webhook payload sent by GitLab on push.
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

func (p GitLabPushEvent) AuthorName() string { return p.Author() }

func (p GitLabPushEvent) Branch() string {
	if strings.HasPrefix(p.Ref, "refs/heads/") {
		return strings.TrimPrefix(p.Ref, "refs/heads/")
	}
	return p.Ref
}

func (p GitLabPushEvent) CommitURL() string {
	if len(p.Commits) > 0 {
		return p.Commits[len(p.Commits)-1].URL
	}
	if p.Project.WebURL != "" && p.SHA() != "" {
		return fmt.Sprintf("%s/-/commit/%s", p.Project.WebURL, p.SHA())
	}
	return ""
}

func (p GitLabPushEvent) ProjectURL() string { return p.Project.WebURL }

func (p GitLabPushEvent) CommitsSummary(maxCommits int) string {
	if len(p.Commits) == 0 {
		return ""
	}
	commits := p.Commits
	if len(commits) > maxCommits {
		commits = commits[len(commits)-maxCommits:]
	}
	lines := make([]string, 0, len(commits)+1)
	for _, c := range commits {
		title := c.Title
		if title == "" {
			title = strings.Split(c.Message, "\n")[0]
		}
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		lines = append(lines, "• "+title)
	}
	if p.TotalCommitsCount > maxCommits {
		lines = append(lines, fmt.Sprintf("...and %d more commits", p.TotalCommitsCount-maxCommits))
	}
	return strings.Join(lines, "\n")
}
