// Test fixture: a tiny HTTP server that echoes a hello message including its
// PID. Used by cloudrun_subprocess_test.go to verify the emulator actually
// spawned a child process and routed traffic to it.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		fmt.Fprintln(os.Stderr, "PORT not set")
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Fixture-Pid", fmt.Sprint(os.Getpid()))
		w.Header().Set("X-K-Service", os.Getenv("K_SERVICE"))
		w.Header().Set("X-Echo-Token", os.Getenv("ECHO_TOKEN"))
		fmt.Fprintf(w, "hello from pid %d: %s", os.Getpid(), body)
	})
	if err := http.ListenAndServe(":"+port, mux); err != nil { //nolint:gosec
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
