package cloudrun

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// runner owns the lifecycle of child processes spawned for cloudrun/functions
// resources. The real Cloud Run runtime boots a container per revision; here we
// just exec a local binary on the first invoke, hand it a port, and proxy
// requests to it. The first invoke pays the cold-start cost; subsequent invokes
// reuse the cached handle.
type runner struct {
	mu       sync.Mutex
	children map[string]*child
}

type child struct {
	cmd     *exec.Cmd
	port    int
	baseURL string
	done    chan struct{}
}

func newRunner() *runner {
	return &runner{children: make(map[string]*child)}
}

// startOrGet returns a running child for name, spawning one if needed.
// command[0] is the executable; command[1:] are its arguments. env is merged on
// top of the parent's environment, with PORT and K_SERVICE always set.
func (r *runner) startOrGet(ctx context.Context, name string, command []string, env map[string]string) (*child, error) {
	if len(command) == 0 {
		return nil, errors.New("command is empty")
	}

	r.mu.Lock()
	if c, ok := r.children[name]; ok {
		select {
		case <-c.done:
			// exited; fall through and respawn
			delete(r.children, name)
		default:
			r.mu.Unlock()
			return c, nil
		}
	}
	r.mu.Unlock()

	port, err := freeTCPPort()
	if err != nil {
		return nil, fmt.Errorf("allocate port: %w", err)
	}

	cmd := exec.Command(command[0], command[1:]...) // #nosec G204 -- command path comes from server config
	cmd.Env = append(os.Environ(),
		"PORT="+strconv.Itoa(port),
		"K_SERVICE="+lastSegment(name),
	)
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command[0], err)
	}

	c := &child{
		cmd:     cmd,
		port:    port,
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		done:    make(chan struct{}),
	}
	go func() {
		_ = cmd.Wait()
		close(c.done)
	}()

	if err := waitListening(ctx, c.port, 10*time.Second); err != nil {
		_ = cmd.Process.Kill()
		<-c.done
		return nil, fmt.Errorf("child %q did not bind port %d: %w", name, c.port, err)
	}

	r.mu.Lock()
	r.children[name] = c
	r.mu.Unlock()
	return c, nil
}

// stop terminates the child for name, if any. Safe to call when no child
// exists.
func (r *runner) stop(name string) {
	r.mu.Lock()
	c, ok := r.children[name]
	if ok {
		delete(r.children, name)
	}
	r.mu.Unlock()
	if !ok {
		return
	}
	killChild(c)
}

// stopAll terminates every running child.
func (r *runner) stopAll() {
	r.mu.Lock()
	all := r.children
	r.children = make(map[string]*child)
	r.mu.Unlock()
	for _, c := range all {
		killChild(c)
	}
}

func killChild(c *child) {
	if c.cmd.Process == nil {
		return
	}
	_ = c.cmd.Process.Kill()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
	}
}

func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		_ = l.Close()
		return 0, fmt.Errorf("unexpected listener addr type %T", l.Addr())
	}
	port := addr.Port
	if err := l.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func waitListening(ctx context.Context, port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := "127.0.0.1:" + strconv.Itoa(port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("timed out after %s", timeout)
}

func lastSegment(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}
