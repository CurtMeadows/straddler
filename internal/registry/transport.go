package registry

import (
	"crypto/tls"
	"net/http"
)

// BuildTransport returns an HTTP transport for registry requests.
// When insecure is true, TLS verification is skipped — use only for
// self-hosted registries with self-signed certificates.
func BuildTransport(insecure bool) http.RoundTripper {
	if insecure {
		return &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		}
	}
	return http.DefaultTransport
}
