package k8s

import (
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/costinm/meshauth/pkg/mdb"
	"github.com/google/go-cmp/cmp"

	core_v1 "k8s.io/api/core/v1"
	discovery_v1 "k8s.io/api/discovery/v1"
	gateway_v1 "sigs.k8s.io/gateway-api/apis/v1"
)

// From the informer cache docs:
// ResourceEventHandler can handle notifications for events that
// happen to a resource. The events are informational only, so you
// can't return an error.  The handlers MUST NOT modify the objects
// received; this concerns not only the top level of structure but all
// the data structures reachable from it.
//   - OnAdd is called when an object is added.
//   - OnUpdate is called when an object is modified. Note that oldObj is the
//     last known state of the object-- it is possible that several changes
//     were combined together, so you can't use this to see every single
//     change. OnUpdate is also called when a re-list happens, and it will
//     get called even if nothing changed. This is useful for periodically
//     evaluating or syncing something.
//   - OnDelete will get the final state of the item if it is known, otherwise
//     it will get an object of type DeletedFinalStateUnknown. This can
//     happen if the watch is closed and misses the delete event and we don't
//     notice the deletion until the subsequent re-list.

// Objects to consider:
//
// Pod
// Node
// Service
// EndpointSlice
// Endpoints
// Gateway
//
// Istio:
// WorkloadEntry
// ServiceEntry
//
//kind: EndpointSlice
//metadata:
//  name: example-abc
//  labels:
//    kubernetes.io/service-name: example
//addressType: IPv4
//ports:
//  - name: http
//    protocol: TCP
//    port: 80
//endpoints:
//  - addresses:
//      - "10.1.2.3"
//    conditions:
//      ready: true
//    hostname: pod-1
//    nodeName: node-1
//    zone: us-west2-a
//
//

// K8SData holds the data for one K8S cluster.
type K8SData struct {
	// Common model for the 'machines' database. Multiple sources of authoritative info are merged.
	MDB *mdb.MDB

	//
	IPToSrc sync.Map
	PodInfo sync.Map

	// set on last 'isInitialList'.
	synced bool

	Pods atomic.Int32

	Slices   atomic.Int32
	SliceIPs atomic.Int32
}

func (k *K8SData) OnAdd(obj interface{}, isInInitialList bool) {
	switch v := obj.(type) {
	case *discovery_v1.EndpointSlice:
		k.Slices.Add(1)
		k.SliceIPs.Add(int32(len(v.Endpoints)))
	case gateway_v1.Gateway:
	case *core_v1.Pod:
		if !isInInitialList {
			slog.Info("POD", "ns", v.Namespace,
				"name", v.Name, "ip", v.Status.PodIP,
				"hostio", v.Status.HostIP, "hostips", v.Status.HostIPs)
		}

		k.PodInfo.Store(v.Name+"."+v.Namespace, v)

		k.Pods.Add(1)

		// TODO: handle multiple IPs
		ip := v.Status.PodIP
		if ip != "" {
			k.IPToSrc.Store(ip, v)
		}
	case *core_v1.Node:
		if isInInitialList {
			slog.Info("NODE", "name", v.Name, "ip", v.Status.Addresses,
				"podCIDR", v.Spec.PodCIDRs)
		} else {
			slog.Info("ADD_NODE", "name", v.Name, "ip", v.Status.Addresses,
				"podCIDR", v.Spec.PodCIDRs)
		}
	}

}

func (k *K8SData) OnUpdate(old, new interface{}) {
	switch v := new.(type) {
	case *core_v1.Pod:
		oldPod := old.(*core_v1.Pod)
		slog.Info(
			"POD_UPDATE", "ns",
			oldPod.Namespace, "name", oldPod.Name, "diff", cmp.Diff(oldPod.Spec, v.Spec))
		ip := v.Status.PodIP
		k.IPToSrc.Store(ip, v)

	case *core_v1.Node:
		if LogNode {
			slog.Info("UPD_NODE", "name", v.Name, "ip", v.Status.Addresses,
				"podCIDR", v.Spec.PodCIDRs)
		}
	}
}

var LogNode = false

func (k *K8SData) OnDelete(obj interface{}) {
	switch v := obj.(type) {
	case *core_v1.Pod:
		k.Pods.Add(-1)
		ip := v.Status.PodIP
		slog.Info("POD DELETED", "ns", v.Namespace, "name", v.Name,
			"ip", ip)
		if ip != "" {
			k.IPToSrc.Delete(ip)
		}
	case *core_v1.Node:
		slog.Info("DEL_NODE", "name", v.Name, "ip", v.Status.Addresses,
			"podCIDR", v.Spec.PodCIDRs)
	}

}
