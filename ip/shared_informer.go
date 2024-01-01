package k8s

// Wrapper for informers - to compare with direct use.

import (
	"fmt"
	"log/slog"
	"time"

	"k8s.io/client-go/informers"
	coreinformers "k8s.io/client-go/informers/core/v1"
	discoveryinformers "k8s.io/client-go/informers/discovery/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"k8s.io/client-go/tools/cache"
)

// K8SIPWatcher watches pods, nodes, services and endpoints/slices to find IPs - using the shared informers.
//
// The informer machinery creates an in-memory replica of the K8S data from etcd, with callbacks on changes.
//
// TODO: also istio serviceentry and workloadentry ?
type K8SIPWatcher struct {
	informerFactory informers.SharedInformerFactory

	// For each type
	nodeInformer  coreinformers.NodeInformer
	podInformer   coreinformers.PodInformer
	epInformer    coreinformers.EndpointsInformer
	sliceInformer discoveryinformers.EndpointSliceInformer

	Agent bool

	// User data structures and callbacks. No direct deps on informers
	*K8SData
}

// Run is used to wait for the sync to happen, and switch from initial sync to 'events' mode.
func (c *K8SIPWatcher) WaitForInit(stopCh chan struct{}) error {
	// wait for the initial synchronization of the local cache.
	if !cache.WaitForCacheSync(stopCh,
		c.podInformer.Informer().HasSynced,
		c.nodeInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to sync")
	}

	c.synced = true
	slog.Info("K8S-Sync", "pods", c.Pods.Load())
	return nil
}

var stop = make(chan struct{})

// Start watching K8S, based on the config. The initial sync will happen in background -
// main() can do other initialization, but before readiness must call WaitForSync.
//
// Will create 'clientsets' using generated code for strong-typed access to objects, as well
// as strong-typed listeners and informer objects.
func Start(kd *K8SData, config *rest.Config) (*K8SIPWatcher, error) {

	//
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	factory := informers.NewSharedInformerFactory(client, time.Hour*24)

	pods, err := NewK8SWatcher(factory)
	if err != nil {
		return nil, err
	}
	pods.K8SData = kd

	go factory.Start(stop)

	return pods, nil
}

// NewK8SWatcher creates a K8SIPWatcher
func NewK8SWatcher(informerFactory informers.SharedInformerFactory) (*K8SIPWatcher, error) {

	podInformer := informerFactory.Core().V1().Pods()
	nodeInformer := informerFactory.Core().V1().Nodes()

	c := &K8SIPWatcher{
		informerFactory: informerFactory,
		podInformer:     podInformer,
		nodeInformer:    nodeInformer,
		K8SData:         &K8SData{},
	}
	_, err := podInformer.Informer().AddEventHandler(c.K8SData)
	if err != nil {
		return nil, err
	}

	_, err = nodeInformer.Informer().AddEventHandler(c.K8SData)
	if err != nil {
		return nil, err
	}

	return c, nil
}
