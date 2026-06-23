package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/chzyer/readline"

	"mole/pkg/client"
	"mole/pkg/config"
)

var Version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "show version")
	serverAddr := flag.String("server", "127.0.0.1:8080", "relay server address")
	clientName := flag.String("name", "", "client name")
	auth := flag.String("auth", "", "auth token")
	allowPossess := flag.Bool("allow-possess", false, "allow other peers to connect")
	localSubnet := flag.String("local-subnet", "", "local subnet to share (auto-detect if empty)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("mole-client %s\n", Version)
		os.Exit(0)
	}

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

	fmt.Printf("=== Mole Client ===\n")
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

	rl, err := readline.NewEx(&readline.Config{
		Prompt: "> ",
		AutoComplete: readline.NewPrefixCompleter(
			readline.PcItem("list"),
			readline.PcItem("connect"),
			readline.PcItem("con"),
			readline.PcItem("disconnect"),
			readline.PcItem("disc"),
			readline.PcItem("possess",
				readline.PcItem("on"),
				readline.PcItem("off"),
			),
			readline.PcItem("status"),
			readline.PcItem("exit"),
			readline.PcItem("quit"),
		),
		HistoryFile:            "/tmp/nt-client-history",
		HistorySearchFold:      true,
	})
	if err != nil {
		log.Fatalf("Readline error: %v", err)
	}
	defer rl.Close()

	fmt.Println("Commands: list, connect|con <peer-id|number>, possess on|off, status, exit")

	for {
		line, err := rl.Readline()
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
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

		case "connect", "con":
			if len(parts) < 2 {
				fmt.Println("Usage: connect|con <peer-id|number>")
			} else if n, err := strconv.Atoi(parts[1]); err == nil {
				if err := c.ConnectByIndex(n); err != nil {
					log.Printf("Connect error: %v", err)
				}
			} else {
				if err := c.ConnectTo(parts[1]); err != nil {
					log.Printf("Connect error: %v", err)
				}
			}

		case "disconnect", "disc":
			if len(parts) < 2 {
				fmt.Println("Usage: disconnect|disc <peer-id|number>")
			} else if n, err := strconv.Atoi(parts[1]); err == nil {
				if err := c.DisconnectByIndex(n); err != nil {
					log.Printf("Disconnect error: %v", err)
				} else {
					fmt.Printf("Disconnected peer #%d\n", n)
				}
			} else {
				c.DisconnectPeer(parts[1])
				fmt.Printf("Disconnected peer %s\n", parts[1])
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
	fmt.Println("Commands: list, connect|con <peer-id|number>, disconnect|disc <peer-id|number>, possess on|off, status, exit")
		}
	}
}
