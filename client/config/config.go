package config

type Config struct {
	ServerAddr   string
	AuthToken    string
	ClientName   string
	AllowPossess bool
	TunnelNet    string
	LocalSubnet  string
}

func DefaultConfig() *Config {
	return &Config{
		ServerAddr:   "localhost:8080",
		AuthToken:    "",
		ClientName:   "",
		AllowPossess: false,
		TunnelNet:    "10.0.0.0/24",
		LocalSubnet:  "",
	}
}
