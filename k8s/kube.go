package k8s

import (
	"context"
	"flag"
	"net"
	"os"
	"strings"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/cert"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

// K8SConfig has general config for a set of clusters.
type K8SConfig struct {
	// Logging options for K8S. Will be set in klog.
	Logging string

	Namespace string

	KSA string
	GSA string
}

// K8S implements the common interface for a set of K8S APIservers
// or servers implementing same patterns.
type K8S struct {
	Config *K8SConfig

	TokenProvider interface{}

	// InCluster (if possible), followed by LoadKubeConfig or GKE config.
	Default *K8SCluster

	// LoadKubeConfig will populate this from a kubeconfig file
	ByName map[string]*K8SCluster
}

// K8SCluster represents a single K8S cluster
type K8SCluster struct {
	// Loaded Config.
	// The URL can be extracted with rest.DefaultServerURLFor(Config)
	// Http client properly configured with rest.HTTPClientFor(Config)
	Config *rest.Config

	// The name should be mangled - gke_PROJECT_LOCATION_NAME or connectgateway_PROJECT_NAME
	// or hostname.
	// Best practice: fleet name, also part of the domain suffix
	// Using the VENDOR_PROJECT_REGION_NAME for all would also be nice.
	Name string

	Namespace string

	// TODO: lazy load. Should be cached.
	Client *kubernetes.Clientset
}

func (k *K8SCluster) GcpInfo() (string, string, string) {
	cf := k.Name
	if strings.HasPrefix(cf, "gke_") {
		parts := strings.Split(cf, "_")
		if len(parts) > 3 {
			// TODO: if env variable with cluster name/location are set - use that for context
			return parts[1], parts[2], parts[3]
		}
	}
	if strings.HasPrefix(cf, "connectgateway_") {
		parts := strings.Split(cf, "_")
		if len(parts) > 2 {
			// TODO: if env variable with cluster name/location are set - use that for context
			// TODO: if opinionanted naming scheme is used for cg names (location-name) - extract it.
			return parts[1], "", parts[2]
		}
	}
	return "", "", cf
}

// Init klog.InitFlags from an env (to avoid messing with the CLI of
// the app). For example -v=9 lists full request content, -v=7 lists requests headers
//func init() {
//	fs := &flag.FlagSet{}
//	klog.InitFlags(fs)
//	kf := strings.Split(os.Getenv("KLOG_FLAGS"), " ")
//	fs.Parse(kf)
//}

// NewK8S will initialize a K8S cluster set.
//
// If running in cluster, the 'local' cluster will be the default.
// Additional clusters can be loaded from istio kubeconfig files, kubeconfig, GKE, Fleet.
func NewK8S(kc *K8SConfig) *K8S {
	if kc == nil {
		kc = &K8SConfig{}
	}
	if kc.Logging == "" {
		kc.Logging = os.Getenv("KLOG_FLAGS")
	}
	SetK8SLogging(kc.Logging)
	return &K8S{Config: kc}
}

// Init klog.InitFlags from an env (to avoid messing with the CLI of
// the app). For example -v=9 lists full request content, -v=7 lists requests headers
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

// LocalCluster returns a cluster determined based on in-cluster or MDS config.
// The extended MDS server is used to cache cluster info to avoid GKE lookups.
// Equivalent to rest.InClusterConfig.
func (kr *K8S) LocalCluster() error {
	token, err := os.ReadFile(tokenFile)
	if err != nil {
		return nil
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

	tlsClientConfig := rest.TLSClientConfig{}

	if _, err := cert.NewPool(rootCAFile); err != nil {
		klog.Errorf("Expected to load root CA config from %s, but got err: %v", rootCAFile, err)
	} else {
		tlsClientConfig.CAFile = rootCAFile
	}

	config := &rest.Config{
		// TODO: switch to using cluster DNS.
		Host:            "https://" + net.JoinHostPort(host, port),
		TLSClientConfig: tlsClientConfig,
		BearerToken:     string(token),
		BearerTokenFile: tokenFile,
	}

	// Rest: in cluster doesn't use kubeconfig
	//config, err := rest.InClusterConfig()
	//if err != nil {
	//	panic(err)
	//}

	kr.Default = &K8SCluster{
		Name:      "incluster",
		Namespace: os.Getenv("POD_NAMESPACE"),
	}

	return kr.Default.InitConfig(config)
}

func (kr *K8SCluster) InitConfig(config *rest.Config) error {

	kr.Config = config

	var err error

	// This is the generated ClientSet - with all possible k8s types...
	// Only used to access the known types in kubernetes interface.
	kr.Client, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	return nil
}

// InitK8SClient will discover a K8S config cluster and return the client.
//
// - KUBE_CONFIG takes priority, is checked first
// - in cluster is probed if KUBE_CONFIG is missing.
//
// Istio Server.initKubeClient handles it for Istio:
// - FileDir fakes it using files (config controller)
// - local MeshConfig from args is read
// - if no configSources or CLI kubeconfig - use it.
func (kr *K8S) InitK8SClient(ctx context.Context) error {
	if kr.Default != nil {
		return nil
	}

	err := kr.LocalCluster()
	if err != nil {
		return err
	}

	err = kr.LoadKubeConfig("")
	if err != nil {
		return err
	}

	return nil
}

// RestClient returns a K8S RESTClient for a specific resource.
// apiPath is typically /api or /apis
// version is v1, etc
// group is "" for core resources.
// Serializer defaults to scheme.Codecs.WithoutConversion()
func (kr *K8SCluster) RestClient(apiPath, version string, group string,
	c runtime.NegotiatedSerializer) (*rest.RESTClient, error) {
	// makes a copy - we won't change the template
	config := *kr.Config

	config.APIPath = apiPath
	config.GroupVersion = &schema.GroupVersion{Version: version, Group: group}
	if c == nil {
		c = scheme.Codecs.WithoutConversion()
	}
	config.NegotiatedSerializer = c

	restClient, err := rest.RESTClientFor(&config)
	return restClient, err
}

func (kr *K8SCluster) ConfigFor(apiPath, version string, group string,
	c runtime.NegotiatedSerializer) *rest.Config {
	// makes a copy - we won't change the template
	config := *kr.Config

	config.APIPath = apiPath
	config.GroupVersion = &schema.GroupVersion{Version: version, Group: group}
	if c == nil {
		c = scheme.Codecs.WithoutConversion()
	}
	config.NegotiatedSerializer = c

	return &config
}

func Is404(err error) bool {
	if se, ok := err.(*k8serrors.StatusError); ok {
		if se.ErrStatus.Code == 404 {
			return true
		}
	}
	return false
}

// GetToken returns a token with the given audience for the current KSA, using CreateToken request.
// Used by the STS token exchanger.
func (kr *K8S) GetToken(ctx context.Context, aud string) (string, error) {
	k := kr.Default
	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: []string{aud},
		},
	}
	ts, err := k.Client.CoreV1().ServiceAccounts(kr.Config.Namespace).CreateToken(ctx,
		kr.Config.KSA, treq, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	return ts.Status.Token, nil
}

func (kr *K8S) GetCM(ctx context.Context, ns string, name string) (map[string]string, error) {
	s, err := kr.Default.Client.CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if Is404(err) {
			err = nil
		}
		return map[string]string{}, err
	}

	return s.Data, nil
}

func (kr *K8S) GetSecret(ctx context.Context, ns string, name string) (map[string][]byte, error) {
	s, err := kr.Default.Client.CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if Is404(err) {
			err = nil
		}
		return map[string][]byte{}, err
	}

	return s.Data, nil
}
