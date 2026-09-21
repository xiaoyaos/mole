package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"mole/pkg/config"
	"mole/pkg/server"
)

var Version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "show version")
	addr := flag.String("listen", ":8080", "server listen address")
	relayPort := flag.Int("relay-port", config.DefaultRelayPort, "TCP data relay port (must match clients)")
	auth := flag.String("auth", "", "authentication token")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mole-server %s\n", Version)
		os.Exit(0)
	}

	cfg := config.DefaultServerConfig()
	cfg.Listen = *addr
	cfg.RelayPort = *relayPort
	cfg.AuthToken = *auth

	if envListen := os.Getenv("NT_LISTEN"); envListen != "" {
		cfg.Listen = envListen
	}
	if envAuth := os.Getenv("NT_AUTH_TOKEN"); envAuth != "" {
		cfg.AuthToken = envAuth
	}
	if value := os.Getenv("NT_RELAY_PORT"); value != "" {
		port, err := strconv.Atoi(value)
		if err != nil {
			log.Fatalf("Invalid NT_RELAY_PORT: %v", err)
		}
		cfg.RelayPort = port
	}
	if cfg.RelayPort < 1 || cfg.RelayPort > 65535 {
		log.Fatal("-relay-port must be between 1 and 65535")
	}
	relayAddress, err := config.RelayAddress(cfg.Listen, cfg.RelayPort)
	if err != nil {
		log.Fatal(err)
	}

	srv, err := server.NewServer(cfg)
	if err != nil {
		log.Fatalf("Failed to create server: %v", err)
	}

	fmt.Printf("=== Mole Relay Server ===\n")
	fmt.Printf("Listen: %s\n", cfg.Listen)
	fmt.Printf("Relay:  %s\n", relayAddress)
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
