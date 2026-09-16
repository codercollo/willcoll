// Viper loader — reads every configs/*.yaml file (later files override earlier
// ones) and then applies environment-var overrides. See configs/config.example.yaml
// for the shape and sandbox defaults; .env.example is the source of truth for
// the env override names.
package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds every setting the API reads. The sub-structs mirror the yaml
// sections in configs/config.example.yaml.
type Config struct {
	DatabaseDSN       string
	DBMaxConns        int32
	DBMaxConnIdleTime time.Duration
	DBMaxConnLifetime time.Duration
	PASETOKey         string
	ServerAddr        string
	SMTP              SMTPConfig
	SMS               SMSConfig
	IntaSend          IntaSendConfig
	Scoring           ScoringConfig
}

// SMTPConfig is the outbound SMTP mail configuration (spec §3.1a).
type SMTPConfig struct {
	Host     string
	Port     string // string so it feeds net.JoinHostPort directly
	Username string
	Password string
	Sender   string
}

// SMSConfig is the outbound SMS gateway configuration (spec §11.3).
type SMSConfig struct {
	APIKey     string
	APISecret  string
	GatewayURL string
	SenderID   string
}

// IntaSendConfig is the IntaSend payment-gateway configuration (§5.5, §12).
type IntaSendConfig struct {
	PublishableKey string
	SecretKey      string
	WebhookSecret  string
	Environment    string
}

// ScoringConfig is the ml-sidecar server-to-server configuration (§13.3).
type ScoringConfig struct {
	SidecarBaseURL string
	SharedSecret   string
}

// Load reads every configs/*.yaml file (later files override earlier ones) and
// then applies environment-var overrides (see .env.example for the names).
func Load() (*Config, error) {
	v := viper.New()

	v.SetConfigType("yaml")
	v.AddConfigPath("configs")

	matches, err := filepath.Glob("configs/*.yaml")
	if err != nil {
		return nil, fmt.Errorf("scan configs: %w", err)
	}
	sort.Strings(matches)

	for _, path := range matches {
		v.SetConfigFile(path)
		if err := v.MergeInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	// Env overrides whose names don't line up 1:1 with their yaml keys
	// (documented in .env.example).
	_ = v.BindEnv("paseto.key", "PASETO_SYMMETRIC_KEY")
	_ = v.BindEnv("scoring.shared_secret", "SIDECAR_SHARED_SECRET")
	_ = v.BindEnv("scoring.sidecar_base_url", "SIDECAR_BASE_URL")

	return &Config{
		DatabaseDSN:       v.GetString("database.dsn"),
		DBMaxConns:        v.GetInt32("database.max_conns"),
		DBMaxConnIdleTime: v.GetDuration("database.max_conn_idle_time"),
		DBMaxConnLifetime: v.GetDuration("database.max_conn_lifetime"),
		PASETOKey:         v.GetString("paseto.key"),
		ServerAddr:        v.GetString("server.addr"),
		SMTP: SMTPConfig{
			Host:     v.GetString("smtp.host"),
			Port:     v.GetString("smtp.port"),
			Username: v.GetString("smtp.username"),
			Password: v.GetString("smtp.password"),
			Sender:   v.GetString("smtp.sender"),
		},
		SMS: SMSConfig{
			APIKey:     v.GetString("sms.api_key"),
			APISecret:  v.GetString("sms.api_secret"),
			GatewayURL: v.GetString("sms.gateway_url"),
			SenderID:   v.GetString("sms.sender_id"),
		},
		IntaSend: IntaSendConfig{
			PublishableKey: v.GetString("intasend.publishable_key"),
			SecretKey:      v.GetString("intasend.secret_key"),
			WebhookSecret:  v.GetString("intasend.webhook_secret"),
			Environment:    v.GetString("intasend.environment"),
		},
		Scoring: ScoringConfig{
			SidecarBaseURL: v.GetString("scoring.sidecar_base_url"),
			SharedSecret:   v.GetString("scoring.shared_secret"),
		},
	}, nil
}
