// Package probe implements the `-healthcheck` flag of the web and worker binaries.
//
// The runtime image is distroless (no shell, no curl), so a container health check has to be
// the application binary itself: it requests its own health endpoint and exits 0 or 1.
package probe

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// URL builds the loopback URL for a listen address such as ":8081" or "0.0.0.0:8081".
func URL(listenAddr, path string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "http://127.0.0.1:8080" + path
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}

// Run requests url and returns a process exit code: 0 healthy, 1 otherwise. A short reason is
// written to the returned message for the caller to print.
func Run(url string) (code int, msg string) {
	if !strings.HasPrefix(url, "http://127.0.0.1") && !strings.HasPrefix(url, "http://[::1]") && !strings.HasPrefix(url, "http://localhost") {
		return 1, "probe only talks to the local process"
	}
	c := http.Client{Timeout: 4 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return 1, fmt.Sprintf("unhealthy: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1, fmt.Sprintf("unhealthy: %s returned %d", url, resp.StatusCode)
	}
	return 0, "healthy"
}
