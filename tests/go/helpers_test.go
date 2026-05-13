package gcplocaltest

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

var defaultClient = &http.Client{Timeout: 500 * time.Millisecond}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newPost(url string) (*http.Request, error) {
	return http.NewRequest(http.MethodPost, url, nil)
}

func doRequest(req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}
