package state

import (
	"errors"
	"fmt"

	"github.com/GuitarWag/gcp-local/internal/config"
)

var ErrNotFound = errors.New("not found")
var ErrAlreadyExists = errors.New("already exists")

type Store interface {
	Get(namespace, key string) ([]byte, error)
	Put(namespace, key string, value []byte) error
	Delete(namespace, key string) error
	List(namespace, prefix string) (map[string][]byte, error)
	Close() error
}

func Open(cfg *config.Config) (Store, error) {
	switch cfg.State {
	case "memory", "":
		return NewMemory(), nil
	case "boltdb":
		path := cfg.StateDir
		if path == "" {
			path = ".gcp-local.db"
		}
		return NewBolt(path)
	default:
		return nil, fmt.Errorf("unknown state backend: %q", cfg.State)
	}
}
