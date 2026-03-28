package httpx

import (
	"net"
	"net/http"
	"time"
)

// Shared HTTP clients with connection pooling.
// Reuses TCP connections + TLS sessions across requests → saves ~100-200ms/req.

var (
	// For streaming requests (long timeout, keep-alive)
	StreamClient = &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     120 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			DisableCompression: true, // SSE should not be compressed
			ForceAttemptHTTP2:  true,
		},
	}

	// For non-streaming requests (shorter timeout, gzip ok)
	APIClient = &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     120 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2: true,
		},
	}
)
