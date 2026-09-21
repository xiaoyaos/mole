package server

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"mole/pkg/config"
)

func reserveLocalPort(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func TestRelayConflictFailsStartupAndReleasesSignalingPort(t *testing.T) {
	busyRelay := reserveLocalPort(t)
	defer busyRelay.Close()
	signal := reserveLocalPort(t)
	signalAddress := signal.Addr().String()
	signal.Close()
	cfg := config.DefaultServerConfig()
	cfg.Listen = signalAddress
	cfg.RelayPort = busyRelay.Addr().(*net.TCPAddr).Port
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.Start(); err == nil || !strings.Contains(err.Error(), "-relay-port") {
		t.Fatalf("expected actionable relay conflict, got %v", err)
	}
	probe, err := net.Listen("tcp", signalAddress)
	if err != nil {
		t.Fatalf("failed startup left signaling port occupied: %v", err)
	}
	probe.Close()
}

func TestSignalingConflictDoesNotLeaveRelayPortOpen(t *testing.T) {
	busySignal := reserveLocalPort(t)
	defer busySignal.Close()
	relay := reserveLocalPort(t)
	relayAddress := relay.Addr().String()
	port := relay.Addr().(*net.TCPAddr).Port
	relay.Close()
	cfg := config.DefaultServerConfig()
	cfg.Listen = busySignal.Addr().String()
	cfg.RelayPort = port
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = srv.Start(); err == nil {
		t.Fatal("signaling conflict must fail startup")
	}
	probe, err := net.Listen("tcp", relayAddress)
	if err != nil {
		t.Fatalf("failed startup left relay port occupied: %v", err)
	}
	probe.Close()
}

func TestCustomRelayPortTransfersDataAndStopsWhenClosed(t *testing.T) {
	reservation := reserveLocalPort(t)
	port := reservation.Addr().(*net.TCPAddr).Port
	reservation.Close()
	cfg := config.DefaultServerConfig()
	cfg.Listen = "127.0.0.1:0"
	cfg.RelayPort = port
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	signal, relay, err := srv.listen()
	if err != nil {
		t.Fatal(err)
	}
	defer signal.Close()
	defer relay.Close()
	if relay.Addr().(*net.TCPAddr).Port != port {
		t.Fatal("configured port was ignored")
	}
	done := make(chan struct{})
	go func() { srv.serveTCPRelay(relay); close(done) }()
	connect := func() net.Conn {
		conn, err := net.DialTimeout("tcp", relay.Addr().String(), time.Second)
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.WriteString(conn, "RELAY custom-port-test\n"); err != nil {
			t.Fatal(err)
		}
		if line, err := bufio.NewReader(conn).ReadString('\n'); err != nil || line != "OK\n" {
			t.Fatalf("handshake: %q %v", line, err)
		}
		return conn
	}
	first, second := connect(), connect()
	defer first.Close()
	defer second.Close()
	if _, err := io.WriteString(first, "Mole relay works"); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("Mole relay works"))
	if _, err := io.ReadFull(second, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != "Mole relay works" {
		t.Fatalf("unexpected relayed data: %q", got)
	}
	relay.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closing listener left the accept loop running")
	}
}
