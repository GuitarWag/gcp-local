package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Project       string         `yaml:"project"`
	Port          int            `yaml:"port"`
	State         string         `yaml:"state"`
	StateDir      string         `yaml:"state_dir"`
	Dashboard     bool           `yaml:"dashboard"`
	DashboardPort int            `yaml:"dashboard_port"`
	TLS           TLS            `yaml:"tls"`
	Services      ServicesConfig `yaml:"services"`
}

// TLS controls whether the gateway listens over HTTPS. Disabled by default.
type TLS struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type ServicesConfig struct {
	Storage       Storage       `yaml:"storage"`
	PubSub        PubSub        `yaml:"pubsub"`
	SecretManager SecretManager `yaml:"secretmanager"`
	Tasks         Tasks         `yaml:"tasks"`
	Scheduler     Scheduler     `yaml:"scheduler"`
	KMS           KMS           `yaml:"kms"`
	Logging       Logging       `yaml:"logging"`
	Monitoring    Monitoring    `yaml:"monitoring"`
	Firestore     Firestore     `yaml:"firestore"`
	BigQuery      BigQuery      `yaml:"bigquery"`
	Bigtable      Bigtable      `yaml:"bigtable"`
	Spanner       Spanner       `yaml:"spanner"`
	Memorystore   Memorystore   `yaml:"memorystore"`
	CloudSQL      CloudSQL      `yaml:"cloudsql"`
	CloudRun      CloudRun      `yaml:"cloudrun"`
	Functions     Functions     `yaml:"functions"`
	Metadata      Metadata      `yaml:"metadata"`
}

type Metadata struct {
	Enabled bool `yaml:"enabled"`
}

type SecretManager struct {
	Enabled bool     `yaml:"enabled"`
	Secrets []Secret `yaml:"secrets"`
}

type Secret struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type Tasks struct {
	Enabled bool `yaml:"enabled"`
}

type Scheduler struct {
	Enabled bool `yaml:"enabled"`
}

type KMS struct {
	Enabled bool `yaml:"enabled"`
}

type Logging struct {
	Enabled bool `yaml:"enabled"`
}

type Monitoring struct {
	Enabled bool `yaml:"enabled"`
}

type Firestore struct {
	Enabled bool `yaml:"enabled"`
}

type BigQuery struct {
	Enabled bool `yaml:"enabled"`
}

type Bigtable struct {
	Enabled bool `yaml:"enabled"`
}

type Spanner struct {
	Enabled bool `yaml:"enabled"`
}

type Memorystore struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type CloudSQL struct {
	Enabled   bool               `yaml:"enabled"`
	BasePort  int                `yaml:"base_port"`
	Instances []CloudSQLInstance `yaml:"instances"`
}

type CloudSQLInstance struct {
	Name     string `yaml:"name"`
	Engine   string `yaml:"engine"`
	Port     int    `yaml:"port"`
	Database string `yaml:"database"`
	Seed     string `yaml:"seed"`
}

type CloudRun struct {
	Enabled bool `yaml:"enabled"`
}

type Functions struct {
	Enabled bool `yaml:"enabled"`
}

type Storage struct {
	Enabled bool     `yaml:"enabled"`
	Buckets []Bucket `yaml:"buckets"`
}

type Bucket struct {
	Name string `yaml:"name"`
	Seed string `yaml:"seed"`
}

type PubSub struct {
	Enabled bool    `yaml:"enabled"`
	Topics  []Topic `yaml:"topics"`
}

type Topic struct {
	Name          string         `yaml:"name"`
	Subscriptions []Subscription `yaml:"subscriptions"`
}

type Subscription struct {
	Name         string `yaml:"name"`
	PushEndpoint string `yaml:"push_endpoint"`
}

func Default() *Config {
	return &Config{
		Project:       "local-project",
		Port:          4443,
		State:         "memory",
		Dashboard:     true,
		DashboardPort: 4444,
		Services: ServicesConfig{
			Storage:       Storage{Enabled: true},
			PubSub:        PubSub{Enabled: true},
			SecretManager: SecretManager{Enabled: true},
			Tasks:         Tasks{Enabled: true},
			Scheduler:     Scheduler{Enabled: true},
			KMS:           KMS{Enabled: true},
			Logging:       Logging{Enabled: true},
			Monitoring:    Monitoring{Enabled: true},
			Firestore:     Firestore{Enabled: true},
			BigQuery:      BigQuery{Enabled: true},
			Bigtable:      Bigtable{Enabled: true},
			Spanner:       Spanner{Enabled: true},
			Memorystore:   Memorystore{Enabled: false},
			CloudSQL:      CloudSQL{Enabled: true, BasePort: 5432},
			CloudRun:      CloudRun{Enabled: true},
			Functions:     Functions{Enabled: true},
			Metadata:      Metadata{Enabled: true},
		},
	}
}

func Load(path string) (*Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Port == 0 {
		cfg.Port = 4443
	}
	if cfg.Project == "" {
		cfg.Project = "local-project"
	}
	if cfg.State == "" {
		cfg.State = "memory"
	}
	return cfg, nil
}
