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
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/compute/metadata"
	"github.com/costinm/meshauth"
	"github.com/costinm/meshauth/pkg/mdsd"
	k8s "github.com/costinm/mk8s"
	"github.com/labstack/gommon/random"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
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
	//*meshauth.MeshCfg `json:inline`

	// Discovered certificates and local identity, metadata.
	Mesh *meshauth.Mesh `json:-`

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

//func (gke *GKE) UpdateK8S(ctx context.Context, k8s *k8s.K8S) error {
//	// Use K8S as a token source, with the default KSA and exchanging to access tokens
//	gke.K8S = k8s
//	gke.authScheme = "gke" + random.String(8)
//	RegisterK8STokenProvider(gke.authScheme, gke)
//
//	return nil
//}


func NewModule(module *meshauth.Module) error {
	ctx := context.Background()


	ma := module.Mesh

	// Bootstrap credentials will also be used to connect to K8S and to get
	// further credentials and configs.
	gke, err := New(ctx, module)
	if err != nil {
		return err
	}

	module.Module = gke

	k := gke.K8S
	//k, err := k8sc.New(ctx, &k8sc.K8SConfig{})

	maCfg := ma.MeshCfg

	startupSpan := trace.SpanFromContext(ctx)
	if startupSpan.IsRecording() {
		// Add info about the context to the start span
		startupSpan.SetAttributes(attribute.Int("k8s_count", len(k.ByName)))
		if k.Default != nil {
			startupSpan.SetAttributes(attribute.String("k8s_ctx", k.Default.Name))
		}
	}

	ma.AuthProviders["k8s"] = k.Default

	fedS := meshauth.NewFederatedTokenSource(&meshauth.STSAuthConfig{
		AudienceSource: gke.ProjectId() + ".svc.id.goog",
		TokenSource:    k,
	})
	// Federated access tokens (for ${PROJECT_ID}.svc.id.goog[ns/ksa]
	// K8S JWT access tokens otherwise.
	ma.AuthProviders["gcp_fed"] = fedS

	if ma.GSA == "" {
		// Use default naming conventions
		ma.GSA = "k8s-" + maCfg.Namespace + "@" + gke.ProjectId() + ".iam.gserviceaccount.com"
	}

	if ma.GSA != "-" {
		audTokenS := meshauth.NewFederatedTokenSource(&meshauth.STSAuthConfig{
			TokenSource:    k,
			GSA:            ma.GSA,
			AudienceSource: gke.ProjectId() + ".svc.id.goog",
		})
		ma.AuthProviders["gcp"] = audTokenS
	} else {
		ma.AuthProviders["gcp"] = fedS
	}

	// Start an emulated MDS server if address is set and not running on GCP
	// MDS emulator/redirector listens on localhost by default, as a sidecar or service.
	//
	if module.Address != "" {
		os.Setenv("GCE_METADATA_HOST", "localhost:15021")

		mux := http.NewServeMux()
		err := mdsd.SetupAgent(module.Mesh, mux)
		if err != nil {
			return err
		}
		go http.ListenAndServe("localhost:15021", mux)
	}

	return nil

}

func GCP(m *meshauth.Mesh) *GKE {
	mm := m.Module("gcp")
	if mm == nil {
		return nil
	}
	if gke, ok := mm.Module.(*GKE); ok {
		return gke
	}
	return nil
}

// New will initialize a default K8S cluster - using the environment.
// It may also load clusters from GKE or HUB, and init metadata and meshauth.
// Use if running with GKE clusters and need GCP features.
// The k8s package can also be used with GKE clusters with minimal deps.
func New(ctx context.Context, mod *meshauth.Module) (*GKE, error) {
	var err error
	ma := mod.Mesh

	gke := &GKE{
		Mesh: ma,
	}

	ks := meshauth.ModuleT[k8s.K8S](mod.Mesh, "k8s") // k8s.GetK8S(mod.Mesh)

	gke.K8S = ks
	mod.Module = gke

	// If user set an address to emulate GCP on VM - use it along with
	// K8S credentials.
	// Otherwise - expect to run on GCP with a real MDS server.
	if mod.Address == "" {
		// If the mesh MDS is not setup - will use metadata server.
		// Note that the first call to metadata.OnGCE will set a static var -
		// it can't be changed. If mod.Address is set - we want to set it
		// with the fake MDS.
		if os.Getenv("GCE_METADATA_HOST") != "" || metadata.OnGCE() {
			// TODO: load from real MDS if explicitly set or on GCP.
			// Using the 'mesh mds' client instead of metadata - it works off GCP and
			// may use a local cache.
			if ks.Default == nil {
				log.Println("On GCP/Cloudrun, using MDS as primary source")
				gke.AccessTokenSource = google.ComputeTokenSource("default")
			} else {
				log.Println("On GKE - using federated tokens from GKE MDS")
				gke.AccessTokenSource = google.ComputeTokenSource("default")
			}
		} else {
			if ks.Default != nil {
				log.Println("Not on GCP, no emulated MDS", ks.Default.Name)
			} else {
				log.Println("Not on GCP, no emulated MDS, no K8S")
			}
		}
	} else {
		// Address is set for the 'emulated' GCP mode.
		if ks.Default == nil {
			return nil, errors.New("Missing credentials source")
		}
		pid := gke.ProjectId()
		// In cluster or kube config found a default cluster.
		// If we have a GSA set for impersonation in the config - use it to get
		// access tokens.
		// Use the K8S cluster's defaults.
		//ks.Default.Namespace = gke.MeshCfg.Namespace
		//ks.Default.KSA = gke.MeshCfg.Name

		gke.TokenSource = meshauth.NewFederatedTokenSource(&meshauth.STSAuthConfig{
			TokenSource:    ks.Default,
			AudienceSource: pid + ".svc.id.goog",
			// If no GSA set - returns the original federated access token, requires perms
			GSA: "k8s-default@" + pid + ".iam.gserviceaccount.com",
		})

		// If a GSA is not set - the access tokens will be federated,
		// and the JWTs will be signed by the GKE cluster.
		ma.AuthProviders["gcp"] = gke.TokenSource

		log.Println("Emulated GCP using kubeconfig", ks.Default.Name, mod.Address, ks.Default.Name, pid)

	}


	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") != "" {
		// Don't load the gcloud adc - can't be used for JWTs.
		creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err == nil {
			gke.AccessTokenSource = creds.TokenSource
			log.Println("Using GAC ", creds.ProjectID, creds.JSON)
		}
	}


	// Allow the K8S SDK to use this as a token source.
	gke.authScheme = "gke" + random.String(8)
	RegisterK8STokenProvider(gke.authScheme, gke)

	// Load GKE project clusters and hub

	if ks.Default == nil {
		err = gke.initGKE(ctx)
		if err != nil {
			log.Println("Failed to init from GKE", err)
		}
	}

	return gke, nil
}

// initGKE is called if the settings include an explicit cluster selection.
// or if no default K8S is found - on VMs or CloudRun without a kube config.
//
// Logic is:
// - check the config.MeshAddr or MESH_URL env variables for a URL
func (gke *GKE) initGKE(ctx context.Context) error {

	// Explicitly set MeshAddr - via env variable or mesh config.

	// Must be a fully qualified URL - project ID is ignored
	meshAddrURL := gke.Mesh.MeshCfg.MeshAddr
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


func (gke *GKE) ProjectId() string {
	return mdsd.Get(gke.Mesh).ProjectID()
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

// GetToken is used by the K8S library to get access tokens for the cluster.
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
func (gcp *GKE) NumericProjectId() string {
	n := mdsd.Get(gcp.Mesh).NumericProjectID()
	if n != "" {
		return n
	}
	return gcp.NumericProjectIdResourceManager()
}

func (gcp *GKE) NumericProjectIdResourceManager() string {

	log.Println("Last resort getting project number - consider saving to mesh-env in K8S or caching. Low QPS allowed")

	pdata := gcp.ProjectData()
	if pdata == nil {
		return ""
	}

	return strconv.Itoa(int(pdata.ProjectNumber))
	// This is in v1 - v3 has it encoded in name.
	// return gcp.MeshCfg.MDS.Project.NumericProjectId
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
	pid := gcp.ProjectId()
	if pid == "" {
		return nil
	}
	pdata, err := cr.Projects.Get(pid).Do()
	if err != nil {
		return nil
	}

	gcp.projectData = pdata
	return pdata
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
				RestConfig: gke.loadRestsConfig(c),
				RawConfig:  c,
				Name:       "gke_" + p + "_" + c.Location + "_" + c.Name,
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
		curl := fmt.Sprintf("/v1/projects/%s/locations/global/gkeMemberships/%s", gke.NumericProjectId(), mn)

		gk := &k8s.K8SCluster{
			Name: ctxName,
			// Connecting via HUB
			RestConfig: gke.hubConfig(curl, ctxName),
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
// This will use the emulated token source.
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
			Name:       "gke_" + configProjectId + "_" + c.Location + "_" + c.Name,
			RestConfig: gke.loadRestsConfig(c),

			// Namespace and KSA are set from the defaults.
			Namespace: gke.Mesh.MeshCfg.Namespace,
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
