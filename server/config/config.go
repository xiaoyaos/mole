package config

type Config struct {
	Listen    string
	AuthToken string
}

func DefaultConfig() *Config {
	return &Config{
		Listen:    ":8080",
		AuthToken: "",
	}
}
