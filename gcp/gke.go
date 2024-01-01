// Copyright 2021 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"

	container "cloud.google.com/go/container/apiv1"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"

	// "google.golang.org/genproto/googleapis/container/v1"

	"google.golang.org/api/option"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	kubeconfig "k8s.io/client-go/tools/clientcmd/api"
)

// Integration with GCP - use metadata server or GCP-specific env variables to auto-configure connection to a
// GKE cluster and extract metadata.

// Using the metadata package, which connects to 169.254.169.254, metadata.google.internal or $GCE_METADATA_HOST (http, no prefix)
// Will attempt to guess if running on GCP if env variable is not set.
//
//Note that requests are using a 2 sec timeout.

// Info about the current user's GCP account and instance.
type GCP struct {
	// Current project ID - tokens are associated with this project
	ProjectId string

	// Can be a region (Cloudrun, K8S) or zone (for VMs, K8S)
	Location string

	// For Cloudrun - instanceid. For VM - hostname. For K8S - pod (without suffix)
	InstanceID string
}

// TODO:  finish hub.
type GKE struct {
	// Current project ID - tokens are associated with this project
	ProjectId string

	// Required for using hub
	ProjectNumber string

	// Project where GKE clusters are located.
	ConfigProjectId string

	// Clusters is populated by UpdateClusters
	Clusters []*GKECluster

	// Active cluster.
	// Set using
	Cluster *GKECluster

	ClusterLocation string

	MeshAddr    *url.URL
	ClusterName string

	// For backward compat, POD_NAMESPACE is set as default, followed by "default"
	Namespace string

	// If set, this account will be used by exchanging current google account tokens
	// with this K8S account
	KSA string

	// --------------- old ----------------
	GSA string

	InCluster bool

	Debug bool

	Client *kubernetes.Clientset
}

// Trust domain for the mesh - based on the config cluster.
func (gke *GKE) TrustDomain() string {
	return gke.ConfigProjectId + ".svc.id.goog"
}

// GKECluster wraps cluster information for a discovered hub or gke cluster.
type GKECluster struct {
	// mangled name
	FullName        string
	ClusterName     string
	ClusterLocation string
	ProjectId       string

	GKECluster *containerpb.Cluster

	restConfig *rest.Config
}

// Returns a rest config for the cluster.
// Similar to the 'in cluster config' - but using MDS auth.
func (gke *GKECluster) RestConfig() *rest.Config {
	return gke.restConfig
}

func (gke *GKECluster) Name() string {
	if gke.FullName != "" {
		return gke.FullName
	}
	return "gke_" + gke.ProjectId + "_" + gke.ClusterLocation + "_" + gke.ClusterName
}

var (
	GCPInitTime time.Duration
)

func NewGKE() *GKE {
	return &GKE{}
}

// DefaultsFromEnvAndMD will attempt to configure ProjectId, ClusterName, ClusterLocation, ProjectNumber, used on GCP
// Metadata server will be tried if env variables don't exist.
func (kr *GKE) DefaultsFromEnvAndMD(ctx context.Context) error {

	// Must be a fully qualified URL - project ID is ignored
	if kr.ClusterName == "" {
		kr.ClusterName = os.Getenv("MESH_URL")
	}

	if kr.ClusterName != "" {
		kr.MeshAddr, _ = url.Parse(kr.ClusterName)

	}

	if kr.ProjectId == "" {
		kr.ProjectId = os.Getenv("PROJECT_ID")
	}

	if kr.ProjectNumber == "" {
		kr.ProjectNumber = os.Getenv("PROJECT_NUMBER")
	}

	if kr.ProjectNumber == "" {
		kr.ProjectNumber, _ = metadata.NumericProjectID()
	}

	if kr.ConfigProjectId == "" {
		kr.ConfigProjectId = os.Getenv("CONFIG_PROJECT_ID")
	}

	if kr.ProjectId == "" {
		kr.ProjectId, _ = metadata.ProjectID()
	}

	if kr.ConfigProjectId == "" {
		kr.ConfigProjectId = kr.ProjectId
	}

	if kr.Namespace == "" {
		// Legacy setting - also used by kubectl
		kr.Namespace = os.Getenv("POD_NAMESPACE")
	}

	//slog.Info("GCP config",
	//	"cluster", kr.ProjectId+"/"+kr.ClusterLocation+"/"+kr.ClusterName,
	//	"namespace", kr.Namespace,
	//	"location", kr.ClusterLocation,
	//	"sinceStart", time.Since(t0))
	return nil
}

func RegionFromMetadata() (string, error) {
	v, err := metadata.Get("instance/region")
	if err != nil {
		return "", err
	}
	vs := strings.SplitAfter(v, "/regions/")
	if len(vs) != 2 {
		return "", fmt.Errorf("malformed region value split into %#v", vs)
	}
	return vs[1], nil
}

// InitGKE will use MDS and env variables to initialize, then connect to GKE to get the
// list of available clusters or the explicitly configured cluster.
//
// It will populate the rest.Config for the cluster if K8S env variable is set.
//
// Will load all clusters otherwise, and select one:
//
//	-
func (kr *GKE) InitGKE(ctx context.Context) error {
	// Avoid direct dependency on GCP libraries - may be replaced by a REST client or different XDS server discovery.
	// Load GCP env variables - will be needed.
	kr.DefaultsFromEnvAndMD(ctx)

	if kr.MeshAddr != nil {
		// Explicitly configured cluster. Will not load all clusters
		if kr.MeshAddr.Scheme == "gke" {
			// Shortcut:
			// gke://costin-asm1/us-central1-c/istio
			kr.ConfigProjectId = kr.MeshAddr.Host
			parts := strings.Split(kr.MeshAddr.Path, "/")
			if len(parts) > 3 {
				kr.ClusterLocation = parts[1]
				kr.ClusterName = parts[2]
			}
		} else if kr.MeshAddr.Host == "container.googleapis.com" {
			// Not using the hub resourceLink format:
			//    container.googleapis.com/projects/wlhe-cr/locations/us-central1-c/clusters/asm-cr
			// 'selfLink' from GKE list API:
			// "https://container.googleapis.com/v1/projects/wlhe-cr/locations/us-west1/clusters/istio"

			if len(kr.MeshAddr.Path) > 1 {
				parts := strings.Split(kr.MeshAddr.Path, "/")
				kr.ConfigProjectId = parts[3]
				kr.ClusterLocation = parts[5]
				kr.ClusterName = parts[7]
			}
		}
		// Explicit override - user specified the full path to the cluster.
		// ~400 ms
		cl, err := kr.GKECluster(ctx, kr.ConfigProjectId, kr.ClusterLocation, kr.ClusterName)
		if err != nil {
			log.Println("Failed in NewForConfig", kr, err)
			return err
		}
		kr.Cluster = cl
	} else {
		cl, err := kr.FindClusters(ctx, kr.ConfigProjectId, "")
		if err != nil {
			log.Println("Failed in NewForConfig", kr, err)
			return err
		}

		kr.PickCluster(ctx, cl)
	}

	// Init additional GCP-specific env, and load the k8s cluster using discovery

	return nil
}

// InitGKE loads GCP-specific metadata and discovers the config cluster.
// This step is skipped if user has explicit configuration for required settings.
//
// Namespace,
// ProjectId, ProjectNumber
// ClusterName, ClusterLocation
func (kr *GKE) PickCluster(ctx context.Context, cll []*GKECluster) error {
	t0 := time.Now()

	// TODO: attempt to get the config project ID from a label on the workload or project
	// (if metadata servere or CR can provide them)

	configProjectID := kr.ProjectId
	configLocation := kr.ClusterLocation

	var cl *GKECluster

	// ~500ms
	//label := "mesh_id"
	// Try to get the region from metadata server. For Cloudrun, this is not the same with the cluster - it may be zonal
	myRegion, _ := RegionFromMetadata()
	if myRegion == "" {
		myRegion = configLocation
	}

	log.Println("Selecting a GKE cluster ", kr.ProjectId, configProjectID, myRegion)

	if len(cll) == 0 {
		return nil // no cluster to use
	}

	cl = findCluster(kr, cll, myRegion, cl)
	// TODO: connect to cluster, find istiod - and keep trying until a working one is found ( fallback )

	kr.Cluster = cl

	kr.ProjectId = configProjectID

	GCPInitTime = time.Since(t0)

	return nil
}

func findCluster(kr *GKE, cll []*GKECluster, myRegion string, cl *GKECluster) *GKECluster {
	if kr.ClusterName != "" {
		for _, c := range cll {
			if myRegion != "" && !strings.HasPrefix(c.ClusterLocation, myRegion) {
				continue
			}
			if c.ClusterName == kr.ClusterName {
				cl = c
				break
			}
		}
		if cl == nil {
			for _, c := range cll {
				if c.ClusterName == kr.ClusterName {
					cl = c
					break
				}
			}
		}
	}

	// First attempt to find a cluster in same region, with the name prefix istio (TODO: label or other way to identify
	// preferred config clusters)
	if cl == nil {
		for _, c := range cll {
			if myRegion != "" && !strings.HasPrefix(c.ClusterLocation, myRegion) {
				continue
			}
			if strings.HasPrefix(c.ClusterName, "istio") {
				cl = c
				break
			}
		}
	}
	if cl == nil {
		for _, c := range cll {
			if myRegion != "" && !strings.HasPrefix(c.ClusterLocation, myRegion) {
				continue
			}
			cl = c
			break
		}
	}
	if cl == nil {
		for _, c := range cll {
			if strings.HasPrefix(c.ClusterName, "istio") {
				cl = c
			}
		}
	}
	// Nothing in same region, pick the first
	if cl == nil {
		cl = cll[0]
	}
	return cl
}

func (kr *GKE) GKECluster(ctx context.Context, p, l, clusterName string) (*GKECluster, error) {
	opts := kr.options("")
	cl, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		log.Println("Failed NewClusterManagerClient", p, l, clusterName, err)
		return nil, err
	}

	for i := 0; i < 5; i++ {
		gcr := &containerpb.GetClusterRequest{
			Name: fmt.Sprintf("projects/%s/locations/%s/cluster/%s", p, l, clusterName),
		}
		c, e := cl.GetCluster(ctx, gcr)
		if e == nil {
			rc := &GKECluster{
				ProjectId:       p,
				ClusterLocation: c.Location,
				ClusterName:     c.Name,
				GKECluster:      c,
				FullName:        "gke_" + p + "_" + c.Location + "_" + c.Name,
				//restConfig:      addClusterConfig(c, p, l, clusterName),
			}
			rc.loadRestsConfig()

			return rc, nil
		}
		time.Sleep(1 * time.Second)
		err = e
	}
	return nil, err
}

func (kr *GKE) UsableSubnetworks(ctx context.Context) {

}

// Find clusters in the hub, using connect gateway.
// Note the 2400 qpm (40 QPS) per project limit - may be best to use a local replica.
// roles/gkehub.viewer to list
// roles/gkehub.gatewayReader for read
// roles/gkehub.gatewayEditor for write
func (kr *GKE) FindHubClusters(ctx context.Context, configProjectId string) ([]*GKECluster, error) {
	opts := kr.options(configProjectId)
	mc, err := gkehub.NewGkeHubMembershipClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	if configProjectId == "" {
		configProjectId = kr.ProjectId
	}
	mr := mc.ListMemberships(ctx, &gkehubpb.ListMembershipsRequest{
		Parent: "projects/" + configProjectId + "/locations/-",
	})
	cl := []*GKECluster{}
	for {
		nxt, err := mr.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		// name:"projects/costin-asm1/locations/global/memberships/asm-auto-1"
		// description:"asm-auto-1"
		// endpoint:{
		//   gke_cluster:{resource_link:"//container.googleapis.com/projects/costin-asm1/locations/us-central1/clusters/asm-auto"}
		//   kubernetes_metadata:{
		//      kubernetes_api_server_version:"v1.27.4-gke.900"
		//      node_provider_id:"gce"
		//      update_time:{seconds:1699841057 nanos:203132290}
		//    }
		// }
		// state:{code:READY}
		// authority:{
		//   issuer:"https://container.googleapis.com/v1/projects/costin-asm1/locations/us-central1/clusters/asm-auto"
		//   workload_identity_pool:"costin-asm1.svc.id.goog"
		//   identity_provider:"https://container.googleapis.com/v1/projects/costin-asm1/locations/us-central1/clusters/asm-auto"}
		// create_time:{seconds:1670874121 nanos:556437629}
		// update_time:{seconds:1699841057 nanos:329498707}
		// external_id:"285e1e15-b323-4524-b4cb-7bd24327dea6"
		// unique_id:"b790260e-5386-4a40-b6cc-3d3052a69f6d"
		// infrastructure_type:MULTI_CLOUD
		// monitoring_config:{
		//    project_id:"costin-asm1"
		//    location:"us-central1"
		//    cluster:"asm-auto"
		//    kubernetes_metrics_prefix:"kubernetes.io"
		//    cluster_hash:"d8a89e89d7fe443c9b40e23dcf1994da2993b2ac28ea452abe0b66d68c5099ac"}

		mna := strings.Split(nxt.Name, "/")
		mn := mna[len(mna)-1]
		ctxName := "connectgateway_" + kr.ProjectId + "_global_" + mn
		curl := fmt.Sprintf("/v1/projects/%s/locations/global/gkeMemberships/%s", kr.ProjectNumber, mn)

		gk := &GKECluster{
			FullName: ctxName,
		}
		gk.addHubConfig(curl, ctxName)
		cl = append(cl, gk)

		kr.Clusters = append(kr.Clusters, gk)
	}

	return cl, nil
}

func (gc *GKECluster) addHubConfig(url string, ctxName string) {
	gc.restConfig = &rest.Config{
		Host: "https://connectgateway.googleapis.com" + url,
		AuthProvider: &kubeconfig.AuthProviderConfig{
			Name: "mds",
		},
	}
}

func (kr *GKE) options(configProjectId string) []option.ClientOption {

	if configProjectId == "" {
		configProjectId = kr.ProjectId
	}

	opts := []option.ClientOption{}

	if configProjectId != kr.ProjectId {
		opts = append(opts, option.WithQuotaProject(configProjectId))
	}

	// Use MDS for token source
	cts := google.ComputeTokenSource("default")

	opts = append(opts, option.WithTokenSource(cts))
	return opts
}

// Updates the list of clusters in the config project.
//
// Requires container.clusters.list
func (kr *GKE) FindClusters(ctx context.Context, configProjectId string, location string) ([]*GKECluster, error) {

	clustersL := []*GKECluster{}
	opts := kr.options(configProjectId)
	if configProjectId == "" {
		configProjectId = kr.ProjectId
	}

	cl, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	if location == "" {
		location = "-"
	}

	clcr := &containerpb.ListClustersRequest{
		Parent: "projects/" + configProjectId + "/locations/" + location,
	}
	clusters, err := cl.ListClusters(ctx, clcr)
	if err != nil {
		return nil, err
	}

	for _, c := range clusters.Clusters {
		c := c
		gc := &GKECluster{
			ProjectId:       configProjectId,
			ClusterName:     c.Name,
			ClusterLocation: c.Location,
			GKECluster:      c,
			//restConfig:      addClusterConfig(c, configProjectId, c.Location, c.Name),
		}
		gc.loadRestsConfig()
		clustersL = append(clustersL, gc)
	}
	return clustersL, nil
}

func (gc *GKECluster) loadRestsConfig() {
	c := gc.GKECluster
	caCert, err := base64.StdEncoding.DecodeString(c.MasterAuth.ClusterCaCertificate)
	if err != nil {
		caCert = nil
	}

	gc.restConfig = &rest.Config{
		Host: c.Endpoint,
		AuthProvider: &kubeconfig.AuthProviderConfig{
			Name: "mds",
		},
	}
	gc.restConfig.TLSClientConfig = rest.TLSClientConfig{CAData: caCert}
}

// Converts a GKE GKECluster to k8s config
// This doesn't work anymore - gcp is no longer compiled.
// Better to use rest.Config directly and skip kube config
//func addClusterConfig(c *containerpb.Cluster, p, l, clusterName string) *rest.Config {
//	kc := kubeconfig.NewConfig()
//
//	caCert, err := base64.StdEncoding.DecodeString(c.MasterAuth.ClusterCaCertificate)
//	if err != nil {
//		caCert = nil
//	}
//
//	ctxName := "gke_" + p + "_" + l + "_" + clusterName
//
//	// We need a KUBECONFIG - tools/clientcmd/api/Config object
//	kc.CurrentContext = ctxName
//	kc.Contexts[ctxName] = &kubeconfig.Context{
//		Cluster:  ctxName,
//		AuthInfo: ctxName,
//	}
//	kc.Clusters[ctxName] = &kubeconfig.Cluster{
//		Server:                   "https://" + c.Endpoint,
//		CertificateAuthorityData: caCert,
//	}
//	kc.AuthInfos[ctxName] = &kubeconfig.AuthInfo{
//		AuthProvider: &kubeconfig.AuthProviderConfig{
//			Name: "mds",
//		},
//	}
//	kc.CurrentContext = ctxName
//
//	rc, _ := restConfig(kc)
//	return rc
//}

//func restConfig(kc *kubeconfig.Config) (*rest.Config, error) {
//	// TODO: set default if not set ?
//	return clientcmd.NewNonInteractiveClientConfig(*kc, "", &clientcmd.ConfigOverrides{}, nil).ClientConfig()
//}
