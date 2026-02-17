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

func msgStarted(projectName, sha, branch, author, commitURL string, projectURL string, commitsSummary string) string {
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

	// Добавляем сводку коммитов если есть
	if commitsSummary != "" {
		msg += fmt.Sprintf("\n\n📝 *Commits:*\n%s", commitsSummary)
	}

	return msg
}

func msgSuccess(projectName, sha, branch, author, commitURL string, duration time.Duration, projectURL string, commitsSummary string) string {
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

func msgFailed(projectName, sha, branch, author, commitURL string, projectURL string, duration time.Duration, err error, commitsSummary string) string {
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
		escape(truncate(err.Error(), 300)),
	)

	if commitsSummary != "" {
		msg += fmt.Sprintf("\n\n📝 *Commits:*\n%s", commitsSummary)
	}

	return msg
}

// Рисуем весь список шагов с иконками
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
			icon = ""
		}
		sb.WriteString(fmt.Sprintf("%s `%s`\n", icon, escape(step.Name())))
	}

	// Прогресс-бар внизу
	done := currentIdx
	if currentStatus == stepDone {
		done = total
	}
	sb.WriteString(fmt.Sprintf("\n📊 %s", progressBar(done, total)))

	return sb.String()
}

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

// escape экранирует спецсимволы для MarkdownV2
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

func sendTelegramAndGetID(text string, tgConf Telegram, bot *tgbotapi.BotAPI) int {
	if !tgConf.Enabled || bot == nil {
		return 0
	}

	params := tgbotapi.Params{
		"chat_id":                  strconv.Itoa(int(tgConf.ChatID)),
		"message_thread_id":        strconv.Itoa(int(tgConf.ThreadID)),
		"text":                     text,
		"parse_mode":               "MarkdownV2",
		"disable_web_page_preview": "true",
	}
	resp, err := bot.MakeRequest("sendMessage", params)
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

func (a *App) editTelegram(messageID int, text string) {
	if !a.Cfg.Telegram.Enabled || messageID == 0 || a.Bot == nil {
		return
	}
	params := tgbotapi.Params{
		"chat_id":                  strconv.Itoa(int(a.Cfg.Telegram.ChatID)),
		"message_id":               strconv.Itoa(messageID),
		"text":                     text,
		"parse_mode":               "MarkdownV2",
		"disable_web_page_preview": "true",
	}
	if res, err := a.Bot.MakeRequest("editMessageText", params); err != nil {
		if res.ErrorCode == 400 {
			return
		}
		log.Printf("telegram edit error: %v", err)
	}
}

// Добавляем метод Name для DeployStep
func (d DeployStep) Name() string {
	return fmt.Sprintf("%s %s", d.Cmd, strings.Join(d.Args, " "))
}
