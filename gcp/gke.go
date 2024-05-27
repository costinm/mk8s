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
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"github.com/costinm/meshauth"
	"github.com/costinm/meshauth/util"
	"github.com/costinm/mk8s/k8s"
	"github.com/labstack/gommon/random"
	"golang.org/x/oauth2"
	crm "google.golang.org/api/cloudresourcemanager/v1"

	container "cloud.google.com/go/container/apiv1"
	containerpb "cloud.google.com/go/container/apiv1/containerpb"
	gkehub "cloud.google.com/go/gkehub/apiv1beta1"

	"cloud.google.com/go/gkehub/apiv1beta1/gkehubpb"

	"golang.org/x/oauth2/google"
	"google.golang.org/api/iterator"

	// "google.golang.org/genproto/googleapis/container/v1"

	"google.golang.org/api/option"

	"k8s.io/client-go/rest"

	kubeconfig "k8s.io/client-go/tools/clientcmd/api"
)

// Integration with GCP - use metadata server or GCP-specific env variables to auto-configure connection to a
// GKE cluster and extract metadata.

// Using the metadata package, which connects to 169.254.169.254, metadata.google.internal or $GCE_METADATA_HOST (http, no prefix)
// Will attempt to guess if running on GCP if env variable is not set.
//
//Note that requests are using a 2 sec timeout.

// TODO:  finish hub.
type GKE struct {
	// MDS is equivalent to the recursive result of the metadata server, but
	// can be part of the config file. Will look in ~/.kube/mds.json for defaults.
	*meshauth.MeshCfg `json:inline`

	// Discovered certificates and local identity, metadata.
	MeshAuth *meshauth.MeshAuth `json:-`

	// Clusters loaded from kube config, env, etc.
	// Default should be set after New().
	K8S *k8s.K8S `json:-`

	// Returns access tokens for a user or service account (via MDS or  default credentials) or federated access tokens (GKE without a paired GSA).
	AccessTokenSource oauth2.TokenSource

	// Raw token source - can be the default K8S cluster.
	TokenSource meshauth.TokenSource

	// Cached project info from CRM- loaded on demand if MDS or env or config are missing
	// to find project number. Still requires ProjectId
	projectData *crm.Project

	authScheme string
}

// GKECluster wraps cluster information for a discovered hub or gke cluster.
type GKECluster struct {
	// mangled name
	FullName string

	ClusterName     string
	ClusterLocation string
	ProjectId       string

	GKECluster *containerpb.Cluster

	restConfig *rest.Config
}

// New will initialize a default K8S cluster - using the environment.
// It may also load clusters from GKE or HUB, and init metadata and meshauth.
// Use if running with GKE clusters and need GCP features.
// The k8s package can also be used with GKE clusters with minimal deps.
func New(ctx context.Context, meshCfg *meshauth.MeshCfg) (*GKE, error) {
	if meshCfg == nil {
		meshCfg = &meshauth.MeshCfg{}
	}
	if meshCfg.MDS == nil {
		meshCfg.MDS = &util.Metadata{}
	}

	gke := &GKE{
		MeshCfg: meshCfg,
	}
	gke.authScheme = "gke" + random.String(8)

	k8s.RegisterTokenProvider(gke.authScheme, gke)

	// Overrides via env variables
	gke.defaultsFromEnv(ctx)

	// First attempt to load 'in cluster' and kubeconfig.
	// May not find any cluster - not an error, Default will not be set.
	ks, err := k8s.New(ctx, &k8s.K8SConfig{
		Namespace: meshCfg.Namespace,
	})
	if err != nil {
		return nil, err
	}
	gke.K8S = ks

	// Load extra metadata from a file - kube config does not have all the GCP info
	// and it's better to use cached and customized value.
	kc := os.Getenv("HOME") + "/.kube/mds.json"
	if _, err := os.Stat(kc); err == nil {
		kcb, err := os.ReadFile(kc)
		if err == nil {
			json.Unmarshal(kcb, gke)
		}
	}

	if gke.MeshCfg.MDS == nil {
		gke.MeshCfg.MDS = &util.Metadata{}
	}

	// At this point, we have a project ID and default K8S cluster if running on GKE
	// or on a VM with kube config.
	// We don't have anything if running on an unconfigured GCP VM or CloudRun.

	if ks.Default == nil || gke.MeshCfg.MDS.Project.ProjectId == "" {
		if os.Getenv("GCE_METADATA_HOST") != "" || metadata.OnGCE() {
			// TODO: load from real MDS if explicitly set or on GCP.
			log.Println("No Kubeconfig, incluster - on GCP, using MDS as primary source")
			gke.defaultsFromMDS(ctx)
			gke.AccessTokenSource = google.ComputeTokenSource("default")
			gke.TokenSource = &meshauth.MDS{UseMDSFullToken: true}
		}
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" {
		// Don't load the gcloud adc - can't be used for JWTs.
		creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err == nil {
			gke.AccessTokenSource = creds.TokenSource
			log.Println("Using GAC")
		}
	}

	pid := gke.ProjectId()

	if ks.Default == nil {
		err := gke.initGKE(ctx)
		if err != nil {
			log.Println("Failed to init from GKE", err)
		}
	}

	if ks.Default != nil {
		// In cluster or kube config found a default cluster.
		// If we have a GSA set for impersonation in the config - use it to get
		// access tokens.
		ks.Default.Namespace = gke.MeshCfg.Namespace
		ks.Default.KSA = gke.MeshCfg.Name

		if gke.TokenSource == nil {
			gke.TokenSource = meshauth.NewFederatedTokenSource(&meshauth.STSAuthConfig{
				TokenSource:    ks.Default,
				AudienceSource: pid + ".svc.id.goog",
				// If no GSA set - returns the original federated access token, requires perms
				GSA: "k8s-default@" + pid + ".iam.gserviceaccount.com",
			})
		}
	}

	return gke, nil
}

// defaultsFromEnv will attempt to configure ProjectId, ClusterName, ClusterLocation, ProjectNumber, used on GCP
func (gke *GKE) defaultsFromEnv(ctx context.Context) error {

	if gke.MeshCfg.MDS.Project.ProjectId == "" {
		gke.MeshCfg.MDS.Project.ProjectId = os.Getenv("PROJECT_ID")
	}

	if gke.MeshCfg.MDS.Project.NumericProjectId == 0 {
		projectNumber := os.Getenv("PROJECT_NUMBER")
		gke.MeshCfg.MDS.Project.NumericProjectId, _ = strconv.Atoi(projectNumber)
	}

	if gke.MeshCfg.Namespace == "" {
		// Legacy setting - also used by kubectl
		gke.MeshCfg.Namespace = os.Getenv("POD_NAMESPACE")
	}

	return nil
}

func (gke *GKE) defaultsFromMDS(ctx context.Context) error {
	// TODO: recursive and cache
	if gke.MeshCfg.MDS.Project.ProjectId == "" {
		gke.MeshCfg.MDS.Project.ProjectId, _ = metadata.ProjectID()
	}

	return nil
}

func (gke *GKE) ProjectId() string {
	if gke.MeshCfg.MDS.Project.ProjectId == "" {
		gke.MeshCfg.MDS.Project.ProjectId, _ = metadata.ProjectID()
	}

	return gke.MeshCfg.MDS.Project.ProjectId
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

func (gcp *GKE) GetToken(ctx context.Context, aud string) (string, error) {
	if aud != "" && gcp.TokenSource != nil {
		return gcp.TokenSource.GetToken(ctx, aud)
	}

	// This is cached by the library.
	// Will make a call to oauth2.googleapis.com/token
	t, err := gcp.AccessTokenSource.Token()
	if err != nil {
		return "", err
	}

	if aud != "" {
		// https://cloud.google.com/docs/authentication/get-id-token#go
		//
		// This has 2 problems:
		// - brings a lot of deps (grpc, etc)
		// - doesn't work with ADC except for service_account ( not user )
		// Internally this just uses jwtSource with google access tokens
		//
		//ts1, err := idtoken.NewTokenSource(ctx, s, option.WithCredentials(gcp.creds))
		//if err != nil {
		//	return "", err
		//}
		//t, err := ts1.Token()

		// jwt.TokenSource relies on a private key that is exchanged using
		//    "urn:ietf:params:oauth:grant-type:jwt-bearer"
		// Useful if we have a private key that can be used to sign the JWTs
		//ts2c := &jwt.Config{
		//	Audience:   s,
		//	UseIDToken: true,
		//	TokenURL:   "",
		//}
		//ts2 := ts2c.TokenSource(ctx)
		//
		//t2, err := ts2.Token()
		//if err != nil {
		//	return "", err
		//}

		idt := t.Extra("id_token")
		if idts, ok := idt.(string); ok {
			return idts, nil
		}
		return "", errors.New("Unsupported ID tokens")
	}

	return t.AccessToken, nil
}

//func (gcp *GKE) ProjectLabels(p string) map[string]string {
//	pdata := gcp.ProjectData(p)
//	if pdata == nil {
//		return nil
//	}
//
//	return pdata.Labels
//}

// Token implements the oauth2.TokenSource interface. It calls the original or
// the delegated one.
func (gcp *GKE) Token() (*oauth2.Token, error) {
	if gcp.AccessTokenSource != nil {
		return gcp.AccessTokenSource.Token()
	}
	t, err := gcp.TokenSource.GetToken(context.Background(), "")
	if err != nil {
		return nil, err
	}
	return &oauth2.Token{AccessToken: t}, nil
}

// Required for using hub, TD and other GCP APIs.
// Should be part of the config, env - or loaded on demand from MDS or resource manager.
func (gcp *GKE) NumericProjectId() int {
	if gcp.MeshCfg.MDS.Project.NumericProjectId == 0 {
		projectNumber, err := metadata.NumericProjectID()
		if err == nil {
			gcp.MeshCfg.MDS.Project.NumericProjectId, _ = strconv.Atoi(projectNumber)
		}
	}

	pdata := gcp.ProjectData()
	if pdata == nil {
		return 0
	}

	gcp.MeshCfg.MDS.Project.NumericProjectId = int(pdata.ProjectNumber)
	// This is in v1 - v3 has it encoded in name.
	return gcp.MeshCfg.MDS.Project.NumericProjectId
}

// ProjectData will fetch the project number and other info from CRM.
// Should be used off GCP and cached - MDS is a better source.
func (gcp *GKE) ProjectData() *crm.Project {
	if gcp.projectData != nil {
		return gcp.projectData
	}

	ctx := context.Background()
	cr, err := crm.NewService(ctx, option.WithTokenSource(gcp))
	if err != nil {
		return nil
	}
	pdata, err := cr.Projects.Get(gcp.MeshCfg.MDS.Project.ProjectId).Do()
	if err != nil {
		return nil
	}

	gcp.projectData = pdata
	return pdata
}

// initGKE is called if the settings include an explicit cluster selection.
// or if no default K8S is found - on VMs or CloudRun without a kube config.
//
// Logic is:
// - check the config.MeshAddr or MESH_URL env variables for a URL
func (gke *GKE) initGKE(ctx context.Context) error {

	// Explicitly set MeshAddr - via env variable or mesh config.

	// Must be a fully qualified URL - project ID is ignored
	meshAddrURL := gke.MeshCfg.MeshAddr
	if meshAddrURL == "" {
		meshAddrURL = os.Getenv("MESH_URL")
	}

	findClusterN := "istio"

	// Explicit configuration - use it to load a single cluster.
	if meshAddrURL != "" {
		meshAddr, _ := url.Parse(meshAddrURL)
		if meshAddr.Scheme == "gke" || meshAddr.Host == "container.googleapis.com" {
			// Shortcut:
			// gke:///....costin-asm1/us-central1-c/istio

			// Not using the hub resourceLink format:
			//    container.googleapis.com/projects/wlhe-cr/locations/us-central1-c/clusters/asm-cr
			// 'selfLink' from GKE list API:
			// "https://container.googleapis.com/v1/projects/wlhe-cr/locations/us-west1/clusters/istio"

			if len(meshAddr.Path) > 1 {
				// Explicit override - user specified the full path to the cluster.
				// ~400 ms
				cl, err := gke.GKECluster(ctx, meshAddr.Path)
				if err != nil {
					log.Println("Failed in NewForConfig", gke, err)
					return err
				}
				gke.K8S.Default = cl
				log.Println("GKE init with explicit config", meshAddrURL)
				return nil
			}
		}

		if meshAddr.Scheme == "hub" { // TODO

		}
		if meshAddr.Scheme == "cluster" { // TODO
			findClusterN = meshAddr.Host
		}
	}

	return gke.autodetect(ctx, findClusterN)
}

// GKECluster returns a GKE cluster by getting the config from ClusterManager
// It is used when a K8S cluster is explicitly configured.
func (gke *GKE) GKECluster(ctx context.Context, p string) (*k8s.K8SCluster, error) {

	opts := gke.options("")

	cl, err := container.NewClusterManagerClient(ctx, opts...)
	if err != nil {
		log.Println("Failed NewClusterManagerClient", p, err)
		return nil, err
	}

	for i := 0; i < 5; i++ {
		gcr := &containerpb.GetClusterRequest{
			Name: p, // fmt.Sprintf("projects/%s/locations/%s/cluster/%s", p, l, clusterName),
		}
		c, e := cl.GetCluster(ctx, gcr)
		if e == nil {

			rc := &k8s.K8SCluster{
				Config:    gke.loadRestsConfig(c),
				RawConfig: c,
				Name:      "gke_" + p + "_" + c.Location + "_" + c.Name,
			}

			return rc, nil
		}
		time.Sleep(1 * time.Second)
		err = e
	}
	return nil, err
}

// Find clusters in the hub, using connect gateway.
// Note the 2400 qpm (40 QPS) per project limit - may be best to use a local replica.
// roles/gkehub.viewer to list
// roles/gkehub.gatewayReader for read
// roles/gkehub.gatewayEditor for write
func (gke *GKE) LoadHubClusters(ctx context.Context, configProjectId string) ([]*k8s.K8SCluster, error) {

	opts := gke.options(configProjectId)
	mc, err := gkehub.NewGkeHubMembershipClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	if configProjectId == "" {
		configProjectId = gke.ProjectId()
	}
	mr := mc.ListMemberships(ctx, &gkehubpb.ListMembershipsRequest{
		Parent: "projects/" + configProjectId + "/locations/-",
	})

	cl := []*k8s.K8SCluster{}
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

		gkeC := nxt.GetEndpoint().GetGkeCluster()
		if gkeC != nil {
			log.Println(" HUB GKE ", gkeC.ResourceLink)
		} else {
			log.Println(" HUB not GKE ", nxt.GetEndpoint())

		}

		mna := strings.Split(nxt.Name, "/")
		mn := mna[len(mna)-1]

		ctxName := "connectgateway_" + gke.ProjectId() + "_global_" + mn
		curl := fmt.Sprintf("/v1/projects/%d/locations/global/gkeMemberships/%s", gke.NumericProjectId(), mn)

		gk := &k8s.K8SCluster{
			Name: ctxName,
			// Connecting via HUB
			Config: gke.hubConfig(curl, ctxName),
		}

		cl = append(cl, gk)

		gke.K8S.ByName[gk.Name] = gk
	}

	return cl, nil
}

func (gke *GKE) hubConfig(url string, ctxName string) *rest.Config {
	return &rest.Config{
		Host: "https://connectgateway.googleapis.com" + url,
		AuthProvider: &kubeconfig.AuthProviderConfig{
			Name: gke.authScheme,
		},
	}
}

func (gke *GKE) options(configProjectId string) []option.ClientOption {

	if configProjectId == "" {
		configProjectId = gke.ProjectId()
	}

	opts := []option.ClientOption{}

	if configProjectId != gke.ProjectId() {
		opts = append(opts, option.WithQuotaProject(configProjectId))
	}

	opts = append(opts, option.WithTokenSource(gke))
	return opts
}

// Updates the list of clusters in the specified GKE project.
//
// Requires container.clusters.list
func (gke *GKE) LoadGKEClusters(ctx context.Context, configProjectId string, location string) ([]*k8s.K8SCluster, error) {

	opts := gke.options(configProjectId)

	if configProjectId == "" {
		configProjectId = gke.ProjectId()
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

	clustersL := []*k8s.K8SCluster{}

	for _, c := range clusters.Clusters {
		c := c

		gkk := &k8s.K8SCluster{
			Name:   "gke_" + configProjectId + "_" + c.Location + "_" + c.Name,
			Config: gke.loadRestsConfig(c),

			// Namespace and KSA are set from the defaults.
			Namespace: gke.MeshCfg.Namespace,
			// KSA: gke.MeshCfg.Name,
		}
		gkk.RawConfig = c
		clustersL = append(clustersL, gkk)

		if gke.K8S.ByName[gkk.Name] == nil {
			gke.K8S.ByName[gkk.Name] = gkk
		}
	}
	return clustersL, nil
}

func (gke *GKE) loadRestsConfig(c *containerpb.Cluster) *rest.Config {
	caCert, err := base64.StdEncoding.DecodeString(c.MasterAuth.ClusterCaCertificate)
	if err != nil {
		caCert = nil
	}

	restConfig := &rest.Config{
		Host: c.Endpoint,
		AuthProvider: &kubeconfig.AuthProviderConfig{
			Name: gke.authScheme,
		},
	}
	restConfig.TLSClientConfig = rest.TLSClientConfig{CAData: caCert}
	return restConfig
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
