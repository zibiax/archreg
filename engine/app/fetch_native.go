//go:build !js

package app

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

var httpClient = &http.Client{Timeout: 30 * time.Second}

func httpGet(u string) ([]byte, error) {
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
