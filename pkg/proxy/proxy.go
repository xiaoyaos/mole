package proxy

import (
	"io"
	"log"
	"net"
	"time"
)

// StartReverseProxy listens on listenAddr. For each connection from client,
// it reads the initial data first, THEN connects to the target and forwards
// immediately — mimicking a locally-originated connection with no delay.
func StartReverseProxy(listenAddr, targetAddr string) error {
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	log.Printf("Reverse proxy listening on %s -> %s", listenAddr, targetAddr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handleConn(conn, targetAddr)
	}
}

func handleConn(client net.Conn, targetAddr string) {
	defer client.Close()
	client.SetDeadline(time.Now().Add(30 * time.Second))

	// Read initial data from client first
	buf := make([]byte, 65536)
	n, err := client.Read(buf)
	if err != nil {
		return
	}
	client.SetDeadline(time.Time{})

	firstChunk := buf[:n]

	// Now connect to target
	target, err := net.DialTimeout("tcp", targetAddr, 10*time.Second)
	if err != nil {
		log.Printf("dial target %s: %v", targetAddr, err)
		return
	}
	defer target.Close()

	// Disable Nagle to send data immediately after handshake
	tcpConn, _ := target.(*net.TCPConn)
	if tcpConn != nil {
		tcpConn.SetNoDelay(true)
	}

	if _, err := target.Write(firstChunk); err != nil {
		return
	}

	// Bidirectional copy
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(target, client)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, target)
		done <- struct{}{}
	}()
	<-done
}
