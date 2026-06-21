package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/network-tunnel/net-tunnel/pkg/config"
	"github.com/network-tunnel/net-tunnel/pkg/client"
)

func main() {
	serverAddr := flag.String("server", "127.0.0.1:8080", "relay server address")
	clientName := flag.String("name", "", "client name")
	auth := flag.String("auth", "", "auth token")
	allowPossess := flag.Bool("allow-possess", false, "allow other peers to connect")
	localSubnet := flag.String("local-subnet", "", "local subnet to share (auto-detect if empty)")
	flag.Parse()

	cfg := config.DefaultClientConfig()
	cfg.ServerAddr = *serverAddr
	cfg.ClientName = *clientName
	cfg.AuthToken = *auth
	cfg.AllowPossess = *allowPossess
	cfg.LocalSubnet = *localSubnet

	if envServer := os.Getenv("NT_SERVER"); envServer != "" {
		cfg.ServerAddr = envServer
	}
	if envName := os.Getenv("NT_NAME"); envName != "" {
		cfg.ClientName = envName
	}
	if envToken := os.Getenv("NT_AUTH_TOKEN"); envToken != "" {
		cfg.AuthToken = envToken
	}
	if envPossess := os.Getenv("NT_ALLOW_POSSESS"); envPossess != "" {
		cfg.AllowPossess = envPossess == "1" || envPossess == "true"
	}
	if envSubnet := os.Getenv("NT_LOCAL_SUBNET"); envSubnet != "" {
		cfg.LocalSubnet = envSubnet
	}

	if cfg.ClientName == "" {
		hostname, _ := os.Hostname()
		cfg.ClientName = hostname
	}

	fmt.Printf("=== Net-Tunnel Client ===\n")
	fmt.Printf("Server:  %s\n", cfg.ServerAddr)
	fmt.Printf("Name:    %s\n", cfg.ClientName)
	fmt.Printf("Possess: %v\n", cfg.AllowPossess)
	if cfg.LocalSubnet != "" {
		fmt.Printf("Subnet:  %s\n", cfg.LocalSubnet)
	}
	fmt.Println()

	c := client.NewClient(cfg)

	go func() {
		if err := c.Start(); err != nil {
			log.Fatalf("Client error: %v", err)
		}
	}()

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Println("Commands: list, connect <peer-id>, possess on|off, status, exit")
	fmt.Print("> ")

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}

		parts := strings.Fields(line)
		cmd := parts[0]

		switch cmd {
		case "exit", "quit":
			fmt.Println("Shutting down...")
			c.Stop()
			return

		case "list":
			if err := c.ListPeers(); err != nil {
				log.Printf("List error: %v", err)
			}

		case "connect":
			if len(parts) < 2 {
				fmt.Println("Usage: connect <peer-id>")
			} else {
				if err := c.ConnectTo(parts[1]); err != nil {
					log.Printf("Connect error: %v", err)
				}
			}

		case "possess":
			if len(parts) < 2 {
				fmt.Printf("Current: %v\n", c.AllowPossess())
				fmt.Println("Usage: possess on|off")
			} else {
				allow := parts[1] == "on" || parts[1] == "1" || parts[1] == "true"
				if err := c.TogglePossess(allow); err != nil {
					log.Printf("Toggle error: %v", err)
				} else {
					fmt.Printf("Possession set to: %v\n", allow)
				}
			}

		case "status":
			fmt.Printf("Client ID: %s\n", c.ClientID())
			fmt.Printf("Tunnel IP: %s\n", c.TunnelIP())
			fmt.Printf("Possess:   %v\n", c.AllowPossess())
			peers := c.Peers()
			fmt.Printf("Peers:     %d\n", len(peers))
			for _, pc := range peers {
				fmt.Printf("  - %s (%s)\n", pc.PeerID()[:8], pc.PeerName())
			}

		default:
			fmt.Printf("Unknown command: %s\n", cmd)
			fmt.Println("Commands: list, connect <peer-id>, possess on|off, status, exit")
		}

		fmt.Print("> ")
	}
}
