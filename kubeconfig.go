package mk8s

import (
	"log/slog"
	"os"

	"k8s.io/client-go/tools/clientcmd"
)

// This is the only dep to the kube.config interface - rest of the code
// is based on rest.Config
//
// It is similar to the defaults - but simplified in behavior.

// Note: v1.26 removed 'gcp' plugin using MDS, replaced with gke-gcloud-auth-plugin
// `gcloud components install gke-gcloud-auth-plugin`
//
// The corresponding kube config is:
//
//    exec:
//      apiVersion: client.authentication.k8s.io/v1beta1
//      command: .../google-cloud-sdk/bin/gke-gcloud-auth-plugin
//
// The k8s_auth_provider can be used to do programmatic token injection, by
// registering a plugin.

// LoadKubeConfig loads a set of clusters defined using kube config:
//
//   - explicitly set as param
//   - KUBECONFIG
//   - $HOME/.kube/config
//
// error is set if KUBECONFIG is set or ~/.kube/config exists but
// fail to load. If the file doesn't exist, err is nil and nothing is loaded.
func (kr *K8S) LoadKubeConfig(configFile string) error {
	// Explicit kube config - use it
	if configFile == "" {
		configFile = os.Getenv("KUBECONFIG")
	}
	if configFile == "" {
		configFile = os.Getenv("HOME") + "/.kube/config"
	}

	if _, err := os.Stat(configFile); err != nil {
		return nil
	}
		// Load the kube config explicitly

		// This is an api.Config - i.e. kubeconfig, but just for this file
		//cf, err := clientcmd.LoadFromFile(kc)
		// clientcmd.Load(kcdata)
		// To get the rest.Config:
		//config := clientcmd.NewNonInteractiveClientConfig(cf, cf.CurrentContext, nil, nil)

		// This will attempt to use in-cluster if kc is empty, otherwise
		// same as BuildConfigFromFlags
		//config, err := clientcmd.BuildConfigFromFlags("", kc)

		// May merge multiple kubeconfigs
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: configFile},
			&clientcmd.ConfigOverrides{ //CurrentContext: "",
			})

		// Get merged config - instead of the ClientConfig which only returns default
		// context.
		apiConfig, err := clientConfig.RawConfig()
		if err != nil {
			return err
		}

		// For each cluster in the config, create a K8SCluster with a valid client.
		// K8S config defines clusters as 'contexts' - associating an endpoint and
		// credentials.
		for contextName, cc := range apiConfig.Contexts {
			k := contextName
			cc := cc

			// This is the native method to create a restConfig using the context name.
			clientcmdClientConfig := clientcmd.NewNonInteractiveClientConfig(apiConfig, k, nil, nil)

			// The config file includes a default namespace too for the context.
			ns, _, _ := clientcmdClientConfig.Namespace()

			// restConfig is what the main library is using.
			restConfig, err := clientcmdClientConfig.ClientConfig()
			if err != nil {
				slog.Warn("Invalid K8S Cluster", "cfg", configFile, "context", k, "cluster", cc.Cluster, "err", err)
				continue
			}
			// Can set restCfg.RateLimiter to replace defaults
			if kr.Config.QPS > 0 {
				restConfig.QPS = kr.Config.QPS // default is 5
			}
			if kr.Config.Burst > 0 {
				restConfig.Burst = kr.Config.Burst // default 10
			}
			kcc := &K8SCluster {
				Name:       k,
				Namespace:  ns,
				RestConfig: restConfig,
				RawConfig: clientcmdClientConfig,
			}

			kr.ByName[k] = kcc

			if kr.Default == nil && k == apiConfig.CurrentContext {
				kr.Default = kcc
			}
		}

		return nil
}
