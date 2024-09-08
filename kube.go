package mk8s

import (
	"context"
	"errors"
	"flag"
	"net"
	"os"
	"strings"
	"sync"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/cert"

	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// K8S implements the common interface for a set of K8S APIservers
// or servers implementing K8S patterns.
type K8S struct {
	// Namespace to use by default
	Namespace string

	// KSA to use by default for getting tokens.
	KSA string

	// QPS overrides the default 5 QPS in the client.
	QPS   float32

	// Burst overrides the default 10 burst in the client.
	Burst int

	// Primary config cluster - current context in config, in-cluster
	// picked by config
	Default *K8SCluster

	// LoadKubeConfig will populate this from a kubeconfig file,
	// followed optionally by GKE or other sources.
	ByName map[string]*K8SCluster
}

var (
	perApp = sync.OnceValue[*K8S](func() *K8S {
		k, _ := New(context.Background(), "", "")
		return k
	})
)

func Default() *K8S {
	return perApp()
}

func NewK8SSet(ctx context.Context, ns, name string) *K8S {
	return &K8S{}
}

// NewK8S will initialize a K8S cluster set.
//
// If running in cluster, the 'local' cluster will be the default.
// Additional clusters can be loaded from istio kubeconfig files, kubeconfig, GKE, Fleet.
func New(ctx context.Context, ns, name string) (*K8S, error) {
	k := &K8S{
		ByName: map[string]*K8SCluster{}}
	err := k.init(ctx)
	logging := os.Getenv("KLOG_FLAGS")
	if logging != "" {
		// Env instead of CLI args
		SetK8SLogging(logging)
	}
	return k, err
}

// init will discover a K8S config cluster and return the client.
//
// - KUBE_CONFIG takes priority, is checked first
// - in cluster is probed if KUBE_CONFIG is missing.
//
// Istio Server.initKubeClient handles it for Istio:
// - FileDir fakes it using files (config controller)
// - local MeshConfig from args is read
// - if no configSources or CLI kubeconfig - use it.
func (kr *K8S) init(ctx context.Context) error {
	defer func() {
		if kr.Default != nil {
			if kr.Default.Namespace == "" {
				kr.Default.Namespace = "default"
			}
			if kr.Default.Name == "" {
				kr.Default.Name = "default"
			}
		}
	}()
	if kr.Default != nil {
		return nil
	}

	err := kr.LoadKubeConfig("")
	if err != nil {
		return err
	}

	err = kr.loadInCluster()
	if err != nil {
		return err
	}

	return nil
}

// Init klog.InitFlags from an env (to avoid messing with the CLI of
// the app). For example -v=9 lists full request content, -v=7 lists requests headers
// TODO: integrate with the dynamic logging.
func SetK8SLogging(flags string) {
	// TODO: dynamic
	fs := &flag.FlagSet{}
	klog.InitFlags(fs)
	kf := strings.Split(flags, " ")
	fs.Parse(kf)
}

// TODO: init using Services/ServiceEntry/Gateway:
// - hostname or IP from SE
// - root from DR - but we don't provide an 'inline' option.

const (
	tokenFile  = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	rootCAFile = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// loadInCluster returns a cluster determined based on in-cluster or MDS config.
// The extended MDS server is used to cache cluster info to avoid GKE lookups.
// Equivalent to rest.InClusterConfig.
func (kr *K8S) loadInCluster() error {
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil
	}

	tlsClientConfig := rest.TLSClientConfig{}

	if _, err := cert.NewPool(rootCAFile); err != nil {
		klog.Errorf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
		return err
	} else {
		tlsClientConfig.CAFile = rootCAFile
	}

	// Instead of checking for host, look for the token files
	// Use 10.0.0.1 as default for host
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	if host == "" {
		host = "10.0.0.1"
	}

	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if len(port) == 0 {
		port = "443"
	}

	config := &rest.Config{
		// TODO: switch to using cluster DNS.
		Host:            "https://" + net.JoinHostPort(host, port),
		TLSClientConfig: tlsClientConfig,
		BearerToken:     string(token),
		BearerTokenFile: tokenFile,
	}

	ic := &K8SCluster{
		Name:       "incluster",
		Namespace:  os.Getenv("POD_NAMESPACE"),
		RestConfig: config,
	}

	kr.ByName[ic.Name] = ic
	if kr.Default == nil {
		kr.Default = ic
	}

	return nil
}

func Is404(err error) bool {
	if se, ok := err.(*k8serrors.StatusError); ok {
		if se.ErrStatus.Code == 404 {
			return true
		}
	}
	return false
}

// GetToken returns a token with the given audience for the default KSA, using CreateToken request.
// Used by the STS token exchanger.
func (kr *K8S) GetToken(ctx context.Context, aud string) (string, error) {
	if kr.Default == nil {
		return "", errors.New("No default cluster")
	}
	return kr.Default.GetTokenRaw(ctx, kr.Default.Namespace, kr.Default.KSA, aud)
}
