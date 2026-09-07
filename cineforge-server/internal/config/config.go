package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Config 汇聚 config.yaml 默认值 + 环境变量覆盖（双通道）。
// 优先级：config.yaml 文件 < CINEFORGE_* 环境变量。
type Config struct {
	HTTP     HTTPConfig     `yaml:"http"`
	GRPC     GRPCConfig     `yaml:"grpc"`
	Database DatabaseConfig `yaml:"database"`
	Redis    RedisConfig    `yaml:"redis"`
	Auth     AuthConfig     `yaml:"auth"`
	MinIO    MinIOConfig    `yaml:"minio"`
	Domains  DomainsConfig  `yaml:"domains"`
	Hermes   HermesConfig   `yaml:"hermes"`
	Agent    AgentConfig    `yaml:"agent"`
	DevSeed  DevSeedConfig  `yaml:"devseed"`
}

// DevSeedConfig 开发账号自动种子（仅本地 dev；生产保持 Enabled=false）。
// 启动时按 phone 幂等创建/确认开发账号，使新拉代码后无需手工造号即可登录联调。
type DevSeedConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Phone       string `yaml:"phone"`
	Password    string `yaml:"password"`
	Username    string `yaml:"username"`
	DisplayName string `yaml:"display_name"`
	Role        string `yaml:"role"`
}

// RedisConfig Celery broker / 项目事件通道地址（P4d run-events pub/sub 用；agent 子模块共用）。
type RedisConfig struct {
	URL string `yaml:"url"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
}

type GRPCConfig struct {
	Addr string `yaml:"addr"`
}

type DatabaseConfig struct {
	DSN string `yaml:"dsn"` // postgres://cineforge:pwd@host:5432/cineforge
}

// AuthConfig 登录令牌相关配置（双通道：config.yaml + CINEFORGE_* env）。
// 生产必须用 env 注入可信 secret；空 secret 时登录签发会按契约返回 503。
type AuthConfig struct {
	// Secret 是私有 2-part access_token 的 HMAC 密钥（对应 legacy AuthConfig.auth_secret）。
	Secret string `yaml:"secret"`
	// SessionTTLSeconds 会话时长，必须在 (0, 8h] 内；三 token 同 lifecycle。
	SessionTTLSeconds int `yaml:"session_ttl_seconds"`
	// TapcanvasJWTSecret 是 HS256 tapcanvas_token 的签名密钥。
	TapcanvasJWTSecret string `yaml:"tapcanvas_jwt_secret"`
	// PlatformJWTPrivateKey 是 RS256 platform_token 的 PEM 私钥（支持 \n 转义）。
	PlatformJWTPrivateKey string `yaml:"platform_jwt_private_key"`
	// PlatformJWTKID 是 platform_token 的 header kid（必须与公钥集匹配）。
	PlatformJWTKID string `yaml:"platform_jwt_kid"`
	// FilePlaybackTokenTTLSeconds 文件播放/下载 token TTL（对齐 legacy file_playback_token_ttl_seconds，
	// 运行时实际取值 clamp 到 [1, 300]；默认 300）。
	FilePlaybackTokenTTLSeconds int `yaml:"file_playback_token_ttl_seconds"`
}

type MinIOConfig struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	UseSSL    bool   `yaml:"use_ssl"`
}

type DomainsConfig struct {
	Server string `yaml:"server"` // 后端 API 域名（CORS 用）
	Web    string `yaml:"web"`    // 前端 OSS 域名（CORS 放行用）
}

type HermesConfig struct {
	// Enabled 是否启用 Hermes（模型网关）；对齐 legacy hermes_enabled。
	Enabled bool `yaml:"enabled"`
	// Addr 模型网关 base_url（对齐 legacy hermes_base_url，含 /v1 段）。
	Addr string `yaml:"addr"`
	// HealthURL 健康探活地址（对齐 legacy hermes_health_url）。
	HealthURL string `yaml:"health_url"`
	// Model 默认模型名（对齐 legacy hermes_model）。
	Model string `yaml:"model"`
	// DashboardURL Hermes 控制台地址（对齐 legacy hermes_dashboard_url，空则在 handler 层按域名填充）。
	DashboardURL string `yaml:"dashboard_url"`
	// WebUIURL Open WebUI 地址（对齐 legacy hermes_webui_url，空则在 handler 层按域名填充）。
	WebUIURL string `yaml:"webui_url"`
	// APIKey Hermes API key（占位 "change-me-hermes-local" 视为未配置）。
	APIKey string `yaml:"api_key"`
}

type AgentConfig struct {
	Addr string `yaml:"addr"` // Python agent 子模块
}

// Default 先落一套可跑的默认值；config.yaml 可覆盖；env 最后覆盖。
func (c *Config) Default() {
	c.HTTP.Addr = "0.0.0.0:8080"
	c.GRPC.Addr = "0.0.0.0:8090"
	c.Database.DSN = ""
	c.Redis.URL = "redis://127.0.0.1:6379/0"
	// Auth：secret 与私钥默认空 → 登录签发按契约返回 503；开发在 .env 注入占位密钥。
	c.Auth.SessionTTLSeconds = 8 * 60 * 60 // 8h（上限）
	c.Auth.FilePlaybackTokenTTLSeconds = 5 * 60 // 对齐 legacy 默认 300s
	c.MinIO.Endpoint = "localhost:9000"
	c.MinIO.Bucket = "cineforge-assets"
	c.MinIO.UseSSL = false
	c.Domains.Server = "cine-forge-server.cnjm.top"
	c.Domains.Web = "cine-forge.cnjm.top"
	c.Hermes.Enabled = true
	c.Hermes.Addr = "http://localhost:8642/v1"
	c.Hermes.HealthURL = "http://localhost:8642/health"
	c.Hermes.Model = "hermes-agent"
	c.Hermes.APIKey = "change-me-hermes-local"
	c.Agent.Addr = "http://localhost:8091"
	// DevSeed 默认关闭；本地 dev 用 .env 注入 CINEFORGE_DEVSEED_* 开启。
	c.DevSeed.Enabled = false
	c.DevSeed.Phone = ""
	c.DevSeed.Password = ""
	c.DevSeed.Username = "dev"
	c.DevSeed.DisplayName = "开发账号"
	c.DevSeed.Role = "admin"
}

// Load 解析 config.yaml（可选，文件不存在时仅用默认值 + env），
// 再以 CINEFORGE_* 环境变量覆盖。生产建议完全由 env 驱动。
func Load(path string) (*Config, error) {
	cfg := &Config{}
	cfg.Default()

	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	cfg.applyEnv()
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v := os.Getenv("CINEFORGE_HTTP_ADDR"); v != "" {
		c.HTTP.Addr = v
	}
	if v := os.Getenv("CINEFORGE_GRPC_ADDR"); v != "" {
		c.GRPC.Addr = v
	}
	if v := os.Getenv("CINEFORGE_DB_DSN"); v != "" {
		c.Database.DSN = v
	}
	if v := os.Getenv("CINEFORGE_REDIS_URL"); v != "" {
		c.Redis.URL = v
	}
	if v := os.Getenv("CINEFORGE_AUTH_SECRET"); v != "" {
		c.Auth.Secret = v
	}
	if v := os.Getenv("CINEFORGE_AUTH_SESSION_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Auth.SessionTTLSeconds = n
		}
	}
	if v := os.Getenv("CINEFORGE_TAPCANVAS_JWT_SECRET"); v != "" {
		c.Auth.TapcanvasJWTSecret = v
	}
	if v := os.Getenv("CINEFORGE_PLATFORM_JWT_PRIVATE_KEY"); v != "" {
		c.Auth.PlatformJWTPrivateKey = v
	}
	if v := os.Getenv("CINEFORGE_PLATFORM_JWT_KID"); v != "" {
		c.Auth.PlatformJWTKID = v
	}
	if v := os.Getenv("CINEFORGE_FILE_PLAYBACK_TOKEN_TTL_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Auth.FilePlaybackTokenTTLSeconds = n
		}
	}
	if v := os.Getenv("CINEFORGE_MINIO_ENDPOINT"); v != "" {
		c.MinIO.Endpoint = v
	}
	if v := os.Getenv("CINEFORGE_MINIO_BUCKET"); v != "" {
		c.MinIO.Bucket = v
	}
	if v := os.Getenv("CINEFORGE_MINIO_ACCESS_KEY"); v != "" {
		c.MinIO.AccessKey = v
	}
	if v := os.Getenv("CINEFORGE_MINIO_SECRET_KEY"); v != "" {
		c.MinIO.SecretKey = v
	}
	if v := os.Getenv("CINEFORGE_MINIO_USE_SSL"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.MinIO.UseSSL = b
		}
	}
	if v := os.Getenv("CINEFORGE_SERVER_DOMAIN"); v != "" {
		c.Domains.Server = v
	}
	if v := os.Getenv("CINEFORGE_WEB_DOMAIN"); v != "" {
		c.Domains.Web = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Hermes.Enabled = b
		}
	}
	if v := os.Getenv("CINEFORGE_HERMES_ADDR"); v != "" {
		c.Hermes.Addr = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_HEALTH_URL"); v != "" {
		c.Hermes.HealthURL = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_MODEL"); v != "" {
		c.Hermes.Model = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_DASHBOARD_URL"); v != "" {
		c.Hermes.DashboardURL = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_WEBUI_URL"); v != "" {
		c.Hermes.WebUIURL = v
	}
	if v := os.Getenv("CINEFORGE_HERMES_API_KEY"); v != "" {
		c.Hermes.APIKey = v
	}
	if v := os.Getenv("CINEFORGE_AGENT_ADDR"); v != "" {
		c.Agent.Addr = v
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.DevSeed.Enabled = b
		}
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_PHONE"); v != "" {
		c.DevSeed.Phone = v
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_PASSWORD"); v != "" {
		c.DevSeed.Password = v
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_USERNAME"); v != "" {
		c.DevSeed.Username = v
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_DISPLAY_NAME"); v != "" {
		c.DevSeed.DisplayName = v
	}
	if v := os.Getenv("CINEFORGE_DEVSEED_ROLE"); v != "" {
		c.DevSeed.Role = v
	}
}
