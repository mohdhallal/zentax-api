package config

import "github.com/mohamadhallal/zentax-api/logger"

type Config struct {
	EnvName          string                 `json:"-"`
	App              AppConfig              `json:"app"`
	Server           ServerConfig           `json:"server"`
	Database         DatabaseConfig         `json:"database"`
	CORS             CORSConfig             `json:"cors"`
	Swagger          SwaggerConfig          `json:"swagger"`
	Metrics          MetricsConfig          `json:"metrics"`
	Log              *logger.Config         `json:"log"`
	NexusInternalAPI NexusInternalAPIConfig `json:"nexusInternalApi"`
}

func (c *Config) IsDevelopment() bool {
	return c.App.Env == "development"
}

type AppConfig struct {
	Env          string `json:"env"`
	Port         int    `json:"port"`
	InternalPort int    `json:"internalPort"`
}

type ServerConfig struct {
	ReadHeaderTimeoutMs int `json:"readHeaderTimeoutMs"`
	WriteTimeoutMs      int `json:"writeTimeoutMs"`
	IdleTimeoutMs       int `json:"idleTimeoutMs"`
	ShutdownTimeoutMs   int `json:"shutdownTimeoutMs"`
}

type DatabaseConfig struct {
	URL                 string `json:"url"`
	PoolMax             int    `json:"poolMax"`
	IdleTimeoutMs       int    `json:"idleTimeoutMs"`
	ConnectionTimeoutMs int    `json:"connectionTimeoutMs"`
	ConnMaxLifetimeMs   int    `json:"connMaxLifetimeMs"`
}

type CORSConfig struct {
	AllowedOrigins   []string `json:"allowedOrigins"`
	AllowedMethods   []string `json:"allowedMethods"`
	AllowedHeaders   []string `json:"allowedHeaders"`
	ExposedHeaders   []string `json:"exposedHeaders"`
	AllowCredentials bool     `json:"allowCredentials"`
	MaxAgeSec        int      `json:"maxAgeSec"`
}

type SwaggerConfig struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type MetricsConfig struct {
	Enabled   bool   `json:"enabled"`
	Namespace string `json:"namespace"`
}

type NexusInternalAPIConfig struct {
	BaseURL        string `json:"baseUrl"`
	TimeoutMs      int    `json:"timeoutMs"`
	DefaultRPS     int    `json:"defaultRps"`
	DefaultKeyType string `json:"defaultKeyType"`
}
