package app

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type stepStatus int

const (
	stepRunning stepStatus = iota
	stepDone
	stepFailed
)

// TelegramNotifier implements Notifier via the Telegram Bot API.
type TelegramNotifier struct {
	cfg Telegram
	bot *tgbotapi.BotAPI
}

// NewTelegramNotifier initialises the bot and returns nil when telegram is disabled.
func NewTelegramNotifier(cfg Telegram) (Notifier, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	bot, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		return nil, fmt.Errorf("telegram: %w", err)
	}
	return &TelegramNotifier{cfg: cfg, bot: bot}, nil
}

func (n *TelegramNotifier) Start(info DeployInfo) DeployHandle {
	msgID := n.sendMsg(
		msgStarted(info.ProjectName, info.SHA, info.Branch, info.Author,
			info.CommitURL, info.ProjectURL, escape(info.CommitsSummary)),
	)
	return &tgHandle{n: n, startMsgID: msgID, info: info}
}

type tgHandle struct {
	n             *TelegramNotifier
	startMsgID    int
	progressMsgID int
	info          DeployInfo
}

// Progress sends the initial steps message on first call, edits it on subsequent calls.
func (h *tgHandle) Progress(idx int, status stepStatus) {
	total := len(h.info.Steps)
	text := msgSteps(h.info.ProjectName, h.info.Steps, idx, total, status)
	if h.progressMsgID == 0 {
		h.progressMsgID = h.n.sendMsg(text)
	} else {
		h.n.editMsg(h.progressMsgID, text)
	}
}

func (h *tgHandle) Success(info DeployInfo) {
	total := len(h.info.Steps)
	if h.progressMsgID > 0 {
		h.n.editMsg(h.progressMsgID,
			msgSteps(h.info.ProjectName, h.info.Steps, total, total, stepDone))
	}
	text := msgSuccess(info.ProjectName, info.SHA, info.Branch, info.Author,
		info.CommitURL, info.Duration, info.ProjectURL, escape(info.CommitsSummary))
	if h.startMsgID > 0 {
		h.n.editMsg(h.startMsgID, text)
	} else {
		h.n.sendMsg(text)
	}
}

func (h *tgHandle) Fail(info DeployInfo) {
	h.n.sendMsg(msgFailed(info.ProjectName, info.SHA, info.Branch, info.Author,
		info.CommitURL, info.ProjectURL, info.Duration, info.Err, escape(info.CommitsSummary)))
}

func (n *TelegramNotifier) sendMsg(text string) int {
	params := tgbotapi.Params{
		"chat_id":                  strconv.FormatInt(n.cfg.ChatID, 10),
		"text":                     text,
		"parse_mode":               "MarkdownV2",
		"disable_web_page_preview": "true",
	}
	if n.cfg.ThreadID != 0 {
		params["message_thread_id"] = strconv.FormatInt(n.cfg.ThreadID, 10)
	}
	resp, err := n.bot.MakeRequest("sendMessage", params)
	if err != nil {
		log.Printf("telegram send error: %v", err)
		return 0
	}
	var result struct {
		MessageID int `json:"message_id"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		log.Printf("telegram parse message_id error: %v", err)
		return 0
	}
	return result.MessageID
}

func (n *TelegramNotifier) editMsg(messageID int, text string) {
	if messageID == 0 {
		return
	}
	params := tgbotapi.Params{
		"chat_id":                  strconv.FormatInt(n.cfg.ChatID, 10),
		"message_id":               strconv.Itoa(messageID),
		"text":                     text,
		"parse_mode":               "MarkdownV2",
		"disable_web_page_preview": "true",
	}
	if res, err := n.bot.MakeRequest("editMessageText", params); err != nil {
		if res != nil && res.ErrorCode == 400 {
			return
		}
		log.Printf("telegram edit error: %v", err)
	}
}

// --- Message formatters ---

func msgStarted(projectName, sha, branch, author, commitURL, projectURL, commitsSummary string) string {
	msg := fmt.Sprintf(
		"🚀 *Deploy started*\n"+
			"> *Project*: [%s](%s)\n\n"+
			"⛓️ Branch: `%s`\n"+
			"📋 SHA: [%s](%s)\n"+
			"👤 Author: `%s`",
		escape(projectName), projectURL,
		escape(branch),
		escape(shortSHA(sha)), commitURL,
		escape(author),
	)
	if commitsSummary != "" {
		msg += fmt.Sprintf("\n\n📝 *Commits:*\n%s", commitsSummary)
	}
	return msg
}

func msgSuccess(projectName, sha, branch, author, commitURL string, duration time.Duration, projectURL, commitsSummary string) string {
	msg := fmt.Sprintf(
		"✅ *Deploy successful*\n"+
			"> Project: [%s](%s)\n\n"+
			"⛓️ Branch: `%s`\n"+
			"📋 SHA: [%s](%s)\n"+
			"👤 Author: `%s`\n"+
			"⏱ Duration: `%s`",
		escape(projectName), projectURL,
		escape(branch),
		escape(shortSHA(sha)), commitURL,
		escape(author),
		escape(duration.Round(time.Second).String()),
	)
	if commitsSummary != "" {
		msg += fmt.Sprintf("\n\n📝 *Commits:*\n%s", commitsSummary)
	}
	return msg
}

func msgFailed(projectName, sha, branch, author, commitURL, projectURL string, duration time.Duration, err error, commitsSummary string) string {
	errText := ""
	if err != nil {
		errText = truncate(err.Error(), 300)
	}
	msg := fmt.Sprintf(
		"❌ *Deploy failed*\n"+
			"> *Project*: [%s](%s)\n\n"+
			"⛓️ Branch: `%s`\n"+
			"📋 SHA: [%s](%s)\n"+
			"👤 Author: `%s`\n"+
			"⏱ Duration: `%s`\n\n"+
			"⚠️ Error:\n```\n%s\n```",
		escape(projectName), projectURL,
		escape(branch),
		escape(shortSHA(sha)), commitURL,
		escape(author),
		escape(duration.Round(time.Second).String()),
		escape(errText),
	)
	if commitsSummary != "" {
		msg += fmt.Sprintf("\n\n📝 *Commits:*\n%s", commitsSummary)
	}
	return msg
}

func msgSteps(projectName string, steps []DeployStep, currentIdx, total int, currentStatus stepStatus) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 *Steps* — `%s`\n\n", escape(projectName)))

	for i, step := range steps {
		var icon string
		switch {
		case i < currentIdx:
			icon = "✔️"
		case i == currentIdx:
			switch currentStatus {
			case stepRunning:
				icon = "⏳"
			case stepFailed:
				icon = "❌"
			case stepDone:
				icon = "✔️"
			}
		default:
			icon = "   "
		}
		sb.WriteString(fmt.Sprintf("%s `%s`\n", icon, escape(step.Name())))
	}

	done := currentIdx
	if currentStatus == stepDone {
		done = total
	}
	sb.WriteString(fmt.Sprintf("\n📊 %s", progressBar(done, total)))
	return sb.String()
}

// --- Formatting helpers ---

func shortSHA(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}

// escape escapes special characters for Telegram MarkdownV2.
func escape(s string) string {
	replacer := strings.NewReplacer(
		"_", "\\_", "*", "\\*", "[", "\\[", "]", "\\]",
		"(", "\\(", ")", "\\)", "~", "\\~", "`", "\\`",
		">", "\\>", "#", "\\#", "+", "\\+", "-", "\\-",
		"=", "\\=", "|", "\\|", "{", "\\{", "}", "\\}",
		".", "\\.", "!", "\\!",
	)
	return replacer.Replace(s)
}

func progressBar(current, total int) string {
	const width = 10
	filled := 0
	if total > 0 {
		filled = (current * width) / total
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return fmt.Sprintf("`[%s]` %d/%d", bar, current, total)
}
