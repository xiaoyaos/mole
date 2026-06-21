package config

type ServerConfig struct {
	Listen    string `yaml:"listen" json:"listen"`
	AuthToken string `yaml:"auth_token" json:"auth_token"`
	TunnelNet string `yaml:"tunnel_net" json:"tunnel_net"`
}

type ClientConfig struct {
	ServerAddr   string `yaml:"server_addr" json:"server_addr"`
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
		AuthToken: "",
	}
}

func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ServerAddr:   "localhost:8080",
		AuthToken:    "",
		ClientName:   "",
		AllowPossess: false,
		TunnelNet:    "10.0.0.0/24",
		LocalSubnet:  "",
		LogLevel:     "info",
	}
}
