package gcp

import (
	"context"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport"
)

// Plugin for K8S client to authenticate with a GCP or equivalent metadata server.
// The provider was removed from the tree, alternative is an exec plugin to include
// in all docker images.

// Register an oauth2 token source. This takes a dep on the oauth2 library, but
// client already depends on it.
// Alternative: set WrapTransport directly on the rest.Config.
func RegisterK8STokenProvider(name string, creds oauth2.TokenSource) {
	rest.RegisterAuthProviderPlugin(name, func(clusterAddress string, config map[string]string, persister rest.AuthProviderConfigPersister) (rest.AuthProvider, error) {
		return &mdsAuth{creds: creds}, nil
	})
}

func RegisterTokenSource(name string, creds TokenSource) {
	rest.RegisterAuthProviderPlugin(name, func(clusterAddress string, config map[string]string, persister rest.AuthProviderConfigPersister) (rest.AuthProvider, error) {
		return &mdsAuth{tokenSource: creds}, nil
	})
}

// TokenSource is a common interface for anything returning Bearer or other kind of tokens.
type TokenSource interface {
	// GetToken for a given audience.
	GetToken(context.Context, string) (string, error)
}

// This is the interface expected by rest client.
type mdsAuth struct {
	creds       oauth2.TokenSource
	tokenSource TokenSource
}

func (m *mdsAuth) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	if m.creds != nil {
		return transport.TokenSourceWrapTransport(m.creds)(rt)
	}
	return &AuthRoundTripper{
		rt:    rt,
		creds: m.tokenSource}
}

func (m mdsAuth) Login() error {
	return nil
}

// AuthRoundTripper add tokens from a token source
type AuthRoundTripper struct {
	rt http.RoundTripper

	// Cached token - currently this is for a single token (not audience specific)
	token string
	exp   time.Time

	creds TokenSource
}

func (m *AuthRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	var err error
	if time.Since(m.exp) > 30*time.Minute {
		m.token, err = m.creds.GetToken(request.Context(), "")
		if err != nil {
			return nil, err
		}
		m.exp = time.Now()
	}

	request.Header.Add("Authorization", "Bearer "+m.token)
	return m.rt.RoundTrip(request)
}
