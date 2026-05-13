package pidfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

type Info struct {
	PID  int    `json:"pid"`
	Port int    `json:"port"`
	Host string `json:"host"`
}

func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp/gcp-local.pid"
	}
	return filepath.Join(home, ".gcp-local", "gcp-local.pid")
}

func Write(info Info) error {
	if err := os.MkdirAll(filepath.Dir(Path()), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0600)
}

func Read() (*Info, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		// support legacy plain-pid format
		if pid, perr := strconv.Atoi(string(data)); perr == nil {
			return &Info{PID: pid}, nil
		}
		return nil, err
	}
	return &info, nil
}

func Remove() error {
	err := os.Remove(Path())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Alive reports whether a process with this pid currently exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return false
	}
	return true
}

// SignalTerm sends SIGTERM to pid.
func SignalTerm(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

func String(info Info) string {
	return fmt.Sprintf("pid=%d host=%s port=%d", info.PID, info.Host, info.Port)
}
