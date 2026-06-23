package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	"mole/pkg/config"
	"mole/pkg/server"
)

var Version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "show version")
	addr := flag.String("listen", ":8080", "server listen address")
	auth := flag.String("auth", "", "authentication token")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mole-server %s\n", Version)
		os.Exit(0)
	}

	cfg := config.DefaultServerConfig()
	cfg.Listen = *addr
	cfg.AuthToken = *auth

	if envListen := os.Getenv("NT_LISTEN"); envListen != "" {
		cfg.Listen = envListen
	}
	if envAuth := os.Getenv("NT_AUTH_TOKEN"); envAuth != "" {
		cfg.AuthToken = envAuth
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	fmt.Printf("=== Mole Relay Server ===\n")
	fmt.Printf("Listen: %s\n", cfg.Listen)
	fmt.Printf("Relay:  %s:8081\n", extractHost(cfg.Listen))
	if cfg.AuthToken != "" {
		fmt.Printf("Auth:   enabled\n")
	} else {
		fmt.Printf("Auth:   disabled\n")
	}
	fmt.Println()

	if err := srv.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func extractHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" {
		host = "0.0.0.0"
	}
	return host
}
