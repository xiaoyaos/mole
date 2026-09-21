package config

import (
	"fmt"
	"net"
	"strconv"
)

const DefaultRelayPort = 8081

type ServerConfig struct {
	Listen    string `yaml:"listen" json:"listen"`
	RelayPort int    `yaml:"relay_port" json:"relay_port"`
	AuthToken string `yaml:"auth_token" json:"auth_token"`
	TunnelNet string `yaml:"tunnel_net" json:"tunnel_net"`
}

type ClientConfig struct {
	ServerAddr   string `yaml:"server_addr" json:"server_addr"`
	RelayPort    int    `yaml:"relay_port" json:"relay_port"`
	AuthToken    string `yaml:"auth_token" json:"auth_token"`
	ClientName   string `yaml:"client_name" json:"client_name"`
	AllowPossess bool   `yaml:"allow_possess" json:"allow_possess"`
	TunnelNet    string `yaml:"tunnel_net" json:"tunnel_net"`
	LocalSubnet  string `yaml:"local_subnet" json:"local_subnet"`
	LogLevel     string `yaml:"log_level" json:"log_level"`
}

func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		Listen:    ":8080",
		RelayPort: DefaultRelayPort,
		AuthToken: "",
	}
}

func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerAddr:   "localhost:8080",
		RelayPort:    DefaultRelayPort,
		AuthToken:    "",
		ClientName:   "",
		AllowPossess: false,
		TunnelNet:    "10.0.0.0/24",
		LocalSubnet:  "",
		LogLevel:     "info",
	}
}

func RelayAddress(signalingAddress string, relayPort int) (string, error) {
	// Preserve existing callers that construct a config without this optional field.
	if relayPort == 0 {
		relayPort = DefaultRelayPort
	}
	if relayPort < 1 || relayPort > 65535 {
		return "", fmt.Errorf("relay port must be between 1 and 65535")
	}
	host, _, err := net.SplitHostPort(signalingAddress)
	if err != nil {
		return "", fmt.Errorf("invalid signaling address: %w", err)
	}
	return net.JoinHostPort(host, strconv.Itoa(relayPort)), nil
}
