package launchdock

import (
	"net/http"

	httpxpkg "github.com/nghiahoang/launchdock/internal/httpx"
)

var (
	StreamClient *http.Client = httpxpkg.StreamClient
	APIClient    *http.Client = httpxpkg.APIClient
)
