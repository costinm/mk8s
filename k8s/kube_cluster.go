package k8s

import (
	"context"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
)

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

	// The default and loaded clusters get namespace from config.
	// It is possible to clone the cluster and use a different set.
	Namespace string
	KSA       string

	// TODO: lazy load. Should be cached.
	client *kubernetes.Clientset

	// RawConfig can be a GCP res.Config
	RawConfig               interface{}
	project, location, name string
}

// Return a new K8S cluster with same config and client, but different default
// namespace and KSA.
func (kr *K8SCluster) WithNamespace(ns, n string) *K8SCluster {
	return &K8SCluster{Config: kr.Config, client: kr.Client(), Name: kr.Name,
		Namespace: ns, KSA: n}
}

func (k *K8SCluster) Location() string {
	if k.location == "" {
		k.GcpInfo()
	}
	return k.location
}

func (k *K8SCluster) GcpInfo() (string, string, string) {
	cf := k.Name
	if strings.HasPrefix(cf, "gke_") {
		parts := strings.Split(cf, "_")
		k.project = parts[1]
		k.location = parts[2]
		k.name = parts[3]
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
			k.project = parts[1]

			// TODO: use registration names that include the location !

			return parts[1], "", parts[2]
		}
	}
	return "", "", cf
}

func (kr *K8SCluster) Client() *kubernetes.Clientset {
	if kr.client == nil {
		kr.initConfig(kr.Config)
	}
	return kr.client
}

func (kr *K8SCluster) initConfig(config *rest.Config) error {

	kr.Config = config

	var err error

	// This is the generated ClientSet - with all possible k8s types...
	// Only used to access the known types in kubernetes interface.
	kr.client, err = kubernetes.NewForConfig(config)
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

func (k *K8SCluster) GetToken(ctx context.Context, aud string) (string, error) {
	if k.KSA == "" {
		k.KSA = "default"
	}
	return k.GetTokenRaw(ctx, k.Namespace, k.KSA, aud)
}

func (k *K8SCluster) GetTokenRaw(ctx context.Context,
	ns, ksa, aud string) (string, error) {

	if ns == "" {
		ns = "default"
	}
	if ksa == "" {
		ksa = "default"
	}

	treq := &authenticationv1.TokenRequest{
		Spec: authenticationv1.TokenRequestSpec{
			Audiences: []string{aud},
		},
	}
	ts, err := k.Client().CoreV1().ServiceAccounts(ns).CreateToken(ctx,
		ksa, treq, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}

	return ts.Status.Token, nil
}

func (kr *K8SCluster) GetCM(ctx context.Context, ns string, name string) (map[string]string, error) {
	s, err := kr.Client().CoreV1().ConfigMaps(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if Is404(err) {
			err = nil
		}
		return map[string]string{}, err
	}

	return s.Data, nil
}

func (kr *K8SCluster) GetSecret(ctx context.Context, ns string, name string) (map[string][]byte, error) {
	s, err := kr.Client().CoreV1().Secrets(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if Is404(err) {
			err = nil
		}
		return map[string][]byte{}, err
	}

	return s.Data, nil
}
