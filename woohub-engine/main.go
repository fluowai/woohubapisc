package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"whatsmeow/api"
	"whatsmeow/config"
	"whatsmeow/db"
	"whatsmeow/storage"
	"whatsmeow/whatsapp"
)

func main() {
	log.Println("==================================================")
	log.Println("  Whatsmeow Engine (Official go.mau.fi/whatsmeow) ")
	log.Println("==================================================")

	cfg := config.LoadConfig()

	// 1. Initialize MinIO
	minioService, err := storage.NewMinioService(cfg)
	if err != nil {
		log.Fatalf("[Main] Failed to initialize MinIO: %v", err)
	}

	// 2. Initialize DB
	dbService, err := db.NewDBService(cfg)
	if err != nil {
		log.Fatalf("[Main] Failed to initialize Database: %v", err)
	}
	defer dbService.Close()

	// 3. Initialize WhatsApp Manager
	waMgr, err := whatsapp.NewWhatsAppManager(cfg, minioService, dbService)
	if err != nil {
		log.Fatalf("[Main] Failed to initialize WhatsApp manager: %v", err)
	}

	// 4. Auto-connect existing device session
	if cfg.AutoStartSession {
		go func() {
			time.Sleep(1 * time.Second)
			log.Println("[Main] Auto-starting WhatsApp session...")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := waMgr.StartSession(ctx); err != nil {
				log.Printf("[Main] StartSession initial notice: %v", err)
			}
		}()
	}

	// 5. Setup Router
	router := api.SetupRouter(cfg, waMgr)

	srv := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%s", cfg.Port),
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("[Main] Server listening on http://0.0.0.0:%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[Main] Server listen error: %v", err)
		}
	}()

	<-stop
	log.Println("[Main] Shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("[Main] Server shutdown error: %v", err)
	}

	log.Println("[Main] Whatsmeow engine exited cleanly.")
}
