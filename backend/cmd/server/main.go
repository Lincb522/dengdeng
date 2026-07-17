package main

import (
	"flag"
	"fmt"
	"log"

	"dengdeng/internal/config"
	"dengdeng/internal/crypto"
	"dengdeng/internal/server"
	"dengdeng/internal/store"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Initialize at-rest encryption for upstream credentials. Falls back to a
	// key derived from JWT_SECRET when ENCRYPTION_KEY is unset.
	if err := crypto.Init(cfg.EncryptionKey, cfg.JWT.Secret); err != nil {
		log.Fatalf("init crypto: %v", err)
	}
	if cfg.EncryptionKey == "" {
		log.Printf("[security] ENCRYPTION_KEY not set; deriving field-encryption key from JWT_SECRET. Set a dedicated ENCRYPTION_KEY (openssl rand -hex 32) in production.")
	}

	db, err := store.Open(cfg)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	if err := store.Seed(db, cfg); err != nil {
		log.Fatalf("seed: %v", err)
	}

	r := server.NewRouter(cfg, db)
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("%s listening on http://%s", cfg.Site.Name, addr)
	if err := r.Run(addr); err != nil {
		log.Fatalf("server: %v", err)
	}
}
