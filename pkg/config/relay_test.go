package config

import "testing"

func TestRelayAddressUsesTheConfiguredPortAndPreservesIPv6(t *testing.T) {
	for _, test := range []struct {
		signal string
		port   int
		want   string
	}{
		{":8080", 18081, ":18081"},
		{"relay.example:8080", 18081, "relay.example:18081"},
		{"[::1]:8080", 18081, "[::1]:18081"},
		{"127.0.0.1:8080", 0, "127.0.0.1:8081"},
	} {
		got, err := RelayAddress(test.signal, test.port)
		if err != nil || got != test.want {
			t.Fatalf("RelayAddress(%q, %d) = %q, %v", test.signal, test.port, got, err)
		}
	}
}

func TestRelayAddressRejectsInvalidConfiguration(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if _, err := RelayAddress(":8080", port); err == nil {
			t.Fatalf("accepted port %d", port)
		}
	}
	if _, err := RelayAddress("missing-port", 18081); err == nil {
		t.Fatal("accepted address without signaling port")
	}
}
