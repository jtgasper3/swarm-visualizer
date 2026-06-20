// Command healthcheck is a tiny client used as the container HEALTHCHECK. The
// final image is built FROM scratch, which has no shell or curl/wget, so this
// compiled helper performs the probe instead. It exits 0 when /healthz returns
// 200 and non-zero otherwise.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	port := os.Getenv("LISTENER_PORT")
	if port == "" {
		port = "8080"
	}

	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%s/healthz", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: unexpected status %d\n", resp.StatusCode)
		os.Exit(1)
	}
}
