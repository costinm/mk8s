package k8s

import (
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"k8s.io/client-go/transport"

	"golang.org/x/oauth2/google"
	"k8s.io/client-go/rest"
)

// Plugin for K8S client to authenticate with a GCP or equivalent metadata server.
//

func init() {
	RegisterTokenProvider("mds", google.ComputeTokenSource("default"))
}

// Register an oauth2 token source. This takes a dep on the oauth2 library, but
// client already depends on it.
// Alternative: set WrapTransport directly on the rest.Config.
func RegisterTokenProvider(name string, creds oauth2.TokenSource) {
	rest.RegisterAuthProviderPlugin(name, func(clusterAddress string, config map[string]string, persister rest.AuthProviderConfigPersister) (rest.AuthProvider, error) {
		return &mdsAuth{creds: creds}, nil
	})
}

// This is the interface expected by rest client.
type mdsAuth struct {
	creds oauth2.TokenSource
}

func (m *mdsAuth) WrapTransport(rt http.RoundTripper) http.RoundTripper {
	if true {
		return transport.TokenSourceWrapTransport(m.creds)(rt)
	}
	return &MDSRoundTripper{
		rt:    rt,
		creds: m.creds}
}

func (m mdsAuth) Login() error {
	return nil
}

// Return a wrapper round tripper.
func OAuth2RoundTripper(rt http.RoundTripper, creds oauth2.TokenSource) http.RoundTripper {
	return &MDSRoundTripper{creds: creds, rt: rt}
}

// Round-tripper adding tokens from an oauth2 source - including MDS server.
type MDSRoundTripper struct {
	rt http.RoundTripper

	// Cached token - currently this is for a single token (not audience specific)
	token string
	exp   time.Time

	creds oauth2.TokenSource
}

func (m *MDSRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	t, err := m.creds.Token()
	//t, err := metadata.Get("/instance/service-accounts/default/token")
	if err != nil {
		return nil, err
	}
	//a := &at{}
	//json.Unmarshal([]byte(t), a)
	request.Header.Add("Authorization", "Bearer "+t.AccessToken)
	return m.rt.RoundTrip(request)
}
