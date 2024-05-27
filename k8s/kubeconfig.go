package k8s

import (
	"log/slog"
	"os"

	"k8s.io/client-go/tools/clientcmd"
)

// This is the only dep to the kube.config interface - rest of the code
// is based on rest.Config

// Note: v1.26 removed 'gcp' plugin using MDS, replaced with gke-gcloud-auth-plugin
// gcloud components install gke-gcloud-auth-plugin - to install
//    exec:
//      apiVersion: client.authentication.k8s.io/v1beta1
//      command: .../google-cloud-sdk/bin/gke-gcloud-auth-plugin
// The k8s_mds_auth restores the old behavior explicitly.

// LoadKubeConfig gets the default k8s client, using environment
// variables to decide how:
//
//   - KUBECONFIG or $HOME/.kube/config will be tried first
//
//   - GKE is checked - using env or metadata server to get
//     PROJECT_ID, CLUSTER_LOCATION, CLUSTER_NAME (if not set), and
//     construct a kube config to use.
//
//   - (in future other vendor-specific methods may be added)
//
//   - finally in-cluster will be checked.
//
// error is set if KUBECONFIG is set or ~/.kube/config exists and
// fail to load. If the file doesn't exist, err is nil.
func (kr *K8S) LoadKubeConfig(kc string) error {
	// Explicit kube config - use it
	if kc == "" {
		kc = os.Getenv("KUBECONFIG")
	}
	if kc == "" {
		kc = os.Getenv("HOME") + "/.kube/config"
	}

	if _, err := os.Stat(kc); err == nil {

		// Load the kube config explicitly
		// This is an api.Config - i.e. kubeconfig, but just for this file
		//cf, err := clientcmd.LoadFromFile(kc)
		// clientcmd.Load(kcdata)
		//config := clientcmd.NewNonInteractiveClientConfig(cf, cf.CurrentContext, nil, nil)

		// Will attempt to use in-cluster if kc is empty, otherwise
		// Same as BuildConfigFromFlags, except no masterUrl used, just kube config which we checked it exists.
		//config, err := clientcmd.BuildConfigFromFlags("", kc)

		cf := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			// May merge multiple kubeconfigs
			&clientcmd.ClientConfigLoadingRules{ExplicitPath: kc},
			&clientcmd.ConfigOverrides{ //CurrentContext: "",
			})

		// Merged config
		cf1, err := cf.RawConfig()
		if err != nil {
			return err
		}

		// For each cluster in the kubeconfig, create a K8SCluster with a valid client.
		for k, cc := range cf1.Contexts {
			//cluster := cf1.Clusters[cc.Cluster]
			//user := cf1.AuthInfos[cc.AuthInfo]
			k := k
			cc := cc

			ctxCfg := clientcmd.NewNonInteractiveClientConfig(cf1, k, nil, nil)
			restCfg, err := ctxCfg.ClientConfig()
			ns, _, _ := ctxCfg.Namespace()

			//rc, err := kubeconfig2Rest(k, cluster, user, cc.Namespace)
			if err != nil {
				slog.Warn("Invalid K8S Cluster", "cfg", kc, "name", k, "cluster", cc.Cluster, "err", err)
			} else {
				kcc := &K8SCluster{
					Name:      k,
					Namespace: ns,
					Config:    restCfg,
				}

				// Can set restCfg.RateLimiter to replace defaults
				restCfg.QPS = 100   // default is 5
				restCfg.Burst = 200 // default 10

				kr.ByName[k] = kcc

				if kr.Default == nil && k == cf1.CurrentContext {
					kr.Default = kcc
				}
			}
		}

		//if kr.Default == nil {
		//	// Will call LoadFromFile(string).
		//	restConfigCurrentContext, err := cf.ClientConfig()
		//	if err != nil {
		//		return err
		//	}
		//	ns, _, _ := cf.Namespace()
		//	kcc := &K8SCluster{
		//		Name:      cf1.CurrentContext,
		//		Namespace: ns,
		//	}
		//	kcc.initConfig(restConfigCurrentContext)
		//	kr.Default = kcc
		//}

		return nil
	}
	return nil
}
