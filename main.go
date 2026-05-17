package main

import (
	"context"
	"fmt"
	"log"
	"main/app"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	fmt.Println("Deploy runner starting... Version: 0.0.0")
	cfg := app.MustLoadConfig("config.yml")

	a, err := app.New(cfg)
	if err != nil {
		log.Fatalf("init error: %v", err)
	}
	defer a.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/deploy", a.DeployHandler)
	mux.HandleFunc("/status", a.StatusHandler)

	server := &http.Server{
		Addr:         ":" + cfg.Server.Port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	log.Println("Deploy runner started on", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
