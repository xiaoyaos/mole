package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
)

func main() {
	listen := flag.String("listen", "0.0.0.0:8080", "local listen address")
	remote := flag.String("remote", "", "remote target address")
	flag.Parse()

	if *remote == "" {
		log.Fatal("-remote is required")
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	host, portStr, _ := net.SplitHostPort(*remote)
	port, _ := strconv.Atoi(portStr)
	if port == 0 {
		port = 80
	}
	remoteAddr := net.JoinHostPort(host, strconv.Itoa(port))

	log.Printf("TCP proxy listening on %s -> %s", *listen, remoteAddr)

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("accept: %v", err)
			continue
		}
		go handle(conn, remoteAddr)
	}
}

func handle(in net.Conn, remoteAddr string) {
	defer in.Close()

	out, err := net.Dial("tcp", remoteAddr)
	if err != nil {
		log.Printf("dial %s: %v", remoteAddr, err)
		return
	}
	defer out.Close()

	done := make(chan struct{}, 2)
	go cp(out, in, done, "->")
	go cp(in, out, done, "<-")
	<-done
}

func cp(dst io.Writer, src io.Reader, done chan struct{}, dir string) {
	n, err := io.Copy(dst, src)
	if err != nil && !isClosed(err) {
		log.Printf("copy %s: %v (%d bytes)", dir, err, n)
	}
	done <- struct{}{}
}

func isClosed(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed network connection") ||
		strings.Contains(s, "broken pipe")
}

func init() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	fmt.Println("TCP Proxy - Forward connections to remote hosts")
}
