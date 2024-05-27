package gcp

import (
	"context"
	"log"
	"strings"

	"github.com/costinm/mk8s/k8s"
)

// If not cluster is explicitly set, try to autodetect a cluster in GKE or HUB
func (gke *GKE) autodetect(ctx context.Context, findClusterN string) error {
	// file or env or MDS
	_, err := gke.LoadGKEClusters(ctx, "", "")
	if err != nil {
		log.Println("Failed loading GKE clusters ", gke, err)
		return err
	}
	_, err = gke.LoadHubClusters(ctx, "")
	if err != nil {
		log.Println("Failed loading HUB clusters", gke, err)
		return err
	}

	// ~500ms
	//label := "mesh_id"
	// Try to get the region from metadata server. For Cloudrun, this is not the same with the cluster - it may be zonal
	myRegion, _ := RegionFromMetadata()

	cl := gke.FindCluster(myRegion, findClusterN)

	if cl != nil {
		log.Println("Found default cluster", cl.Name)
	}

	// TODO: connect to cluster, find istiod - and keep trying until a working one is found ( fallback )

	gke.K8S.Default = cl
	return nil
}

// FindCluster will iterate all loaded clusters and find a 'default'.
// This happens on Cloudrun or VMs without a kubeconfig or explicit
// cluster configured.
//
// - will attempt to find a cluster in the same region
// - if nothing - pick any cluster.
func (kr *GKE) FindCluster(myRegion, clusterName string) *k8s.K8SCluster {
	var cl *k8s.K8SCluster

	// TODO: probe the clusters, remove bad ones

	if clusterName != "" {
		for _, c := range kr.K8S.ByName {
			if myRegion != "" && !strings.HasPrefix(c.Location(), myRegion) {
				continue
			}
			if strings.Contains(c.Name, clusterName) {
				cl = c
				log.Println("Found cluster with region and name ", myRegion, clusterName, c.Name)
				break
			}
		}

		if cl == nil {
			for _, c := range kr.K8S.ByName {
				if strings.Contains(c.Name, clusterName) {
					cl = c
					log.Println("Found cluster with name ", clusterName, c.Name)
					break
				}
			}
		}
	}

	// First attempt to find a cluster in same region, with the name prefix istio (TODO: label or other way to identify
	// preferred config clusters)
	if cl == nil {
		for _, c := range kr.K8S.ByName {
			if myRegion != "" && !strings.HasPrefix(c.Location(), myRegion) {
				continue
			}
			log.Println("Found cluster with region ", myRegion, c.Name)
			cl = c
			break
		}
	}
	if cl == nil {
		for _, c := range kr.K8S.ByName {
			cl = c
			break
		}
	}
	return cl
}
