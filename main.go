package main

import (
	"fmt"
	"log"
	"main/app"
	"net/http"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	fmt.Println("Deploy runner starting... Version: 0.0.0")
	cfg := app.MustLoadConfig("config.yml")

	var bot *tgbotapi.BotAPI
	if cfg.Telegram.Enabled {
		var err error
		bot, err = tgbotapi.NewBotAPI(cfg.Telegram.BotToken)
		if err != nil {
			log.Fatalf("telegram init failed: %v", err)
		}
	}

	application := app.NewApp(cfg, bot)

	mux := http.NewServeMux()
	mux.HandleFunc("/deploy", application.DeployHandler)

	server := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Println("Deploy runner started on", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
