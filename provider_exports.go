package launchdock

import providerspkg "github.com/nghiahoang/launchdock/internal/providers"

type Provider = providerspkg.Provider
type OpenAIProvider = providerspkg.OpenAIProvider
type AnthropicProvider = providerspkg.AnthropicProvider
type Pool = providerspkg.Pool

func RouteProvider(providers []Provider, model string) Provider {
	return providerspkg.RouteProvider(providers, model)
}
func ModelToProvider(model string) string { return providerspkg.ModelToProvider(model) }
func NewPool(creds []Credential) *Pool    { return providerspkg.NewPool(creds) }
func PrefixTools(body []byte, prefix string) ([]byte, error) {
	return providerspkg.PrefixTools(body, prefix)
}
func StripToolPrefix(data []byte, prefix string) []byte {
	return providerspkg.StripToolPrefix(data, prefix)
}
func EnsureOAuthRequirements(body []byte) ([]byte, error) {
	return providerspkg.EnsureOAuthRequirements(body)
}
