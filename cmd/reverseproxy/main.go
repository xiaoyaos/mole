package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/network-tunnel/net-tunnel/pkg/proxy"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8080", "local listen address")
	target := flag.String("target", "", "remote target address (host:port)")
	flag.Parse()

	if *target == "" {
		log.Fatal("-target is required")
	}

	fmt.Printf("=== Reverse Proxy ===\n")
	fmt.Printf("Listen: %s\n", *listen)
	fmt.Printf("Target: %s\n", *target)
	fmt.Println()

	if err := proxy.StartReverseProxy(*listen, *target); err != nil {
		log.Fatalf("proxy error: %v", err)
	}
}
