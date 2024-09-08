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
	"fmt"
	"log"
	"testing"
	"time"

	"github.com/costinm/meshauth"
	k8sc "github.com/costinm/mk8s"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// Requires real or local metadata server.
// The GCP SA must have k8s api permission.
func TestK8S(t *testing.T) {
	// For the entire test
	ctx, cf := context.WithTimeout(context.Background(), 100*time.Second)
	defer cf()

	// This or iptables redirect for 169.254.169.254.
	// When running in GKE or GCP - want to use the custom MDS not the platform one.
	// This must be set before metadata package is used - it detects.
	//if os.Getenv("GCE_METADATA_HOST") == "" {
	//	os.Setenv("GCE_METADATA_HOST", "localhost:15014")
	//}

	ma := meshauth.New(nil)

	gke, err := New(ctx, &meshauth.Module{Mesh: ma})
	if err != nil {
		t.Fatal(err)
	}

	// Basic access token -
	access, err := gke.GetToken(ctx, "")
	if err != nil {
		t.Fatal(err)
	}

	// Should be a federated token
	t.Log("Token", "federated access", access[0:7])

		// Verify resource manager works
		t.Log("On demand number:", gke.NumericProjectIdResourceManager())

	// Can't return JWT tokens signed by google for federated identities, but K8S can.
	access, err = gke.K8S.Default.GetToken(ctx, "dummy")
	if err != nil {
		t.Fatal(err)
	}
	j := meshauth.DecodeJWT(access)
	t.Log("Token", "k8s", j.String())

	istio_ca, err := gke.GetToken(ctx, "istio_ca")
	if err != nil {
		t.Fatal(err)
	}

	j = meshauth.DecodeJWT(istio_ca)
	t.Log("Token", "jwt", j.String())

	// GcpInit should also populate project ID.
	t.Log("Meta", "projectID", gke.ProjectId())

	// Update the list of clusters.
	cl, err := gke.LoadGKEClusters(ctx, "", "us-central1")
	if err != nil {
		t.Fatal(err)
	}

	if len(cl) == 0 {
		t.Fatal("No clusters in " + gke.ProjectId())
	}

	cl, err = gke.LoadGKEClusters(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) == 0 {
		t.Fatal("No clusters in " + gke.ProjectId())
	}

	gke.LoadHubClusters(ctx, "")

	testCluster := gke.K8S.Default

	for _, c := range gke.K8S.ByName {
		//if strings.Contains(c.Name, "big1") {
		//	testCluster = c
		//}
		c := c
		checkClient(c)
	}

	//rc := testCluster.Config

	//log.Printf("%v\n%v\n", testCluster.Name, rc)

	// Run the tests on the first found cluster, unless the test is run with env variables to select a specific
	// location and cluster name.

	// Explicit GKE project configured. Will find some credentials and use it
	t.Run("configClusterExplicit", func(t *testing.T) {
		p, l, n := testCluster.GcpInfo()
		meshAddr := fmt.Sprintf("https://container.googleapis.com/v1/projects/%s/locations/%s/clusters/%s", p, l, n)

		ma := meshauth.New(&meshauth.MeshCfg{MeshAddr: meshAddr})
		kr1, err := New(ctx, &meshauth.Module{Mesh: ma})
		if err != nil {
			t.Fatal(err)
		}

		err = checkClient(kr1.K8S.Default)
		if err != nil {
			t.Fatal(err)
		}
	})

	// A GKE project provided - will list it and pick best cluster.
	t.Run("explicitGKEFind", func(t *testing.T) {
		ma := meshauth.New(&meshauth.MeshCfg{MeshAddr: "gke://" + gke.ProjectId()})
		kr1, err := New(ctx, &meshauth.Module{Mesh: ma})
		if err != nil {
			t.Fatal(err)
		}

		err = checkClient(kr1.K8S.Default)
		if err != nil {
			t.Fatal(err)
		}
	})

}

func checkClient(kc *k8sc.K8SCluster) error {
	v, err := kc.Client().ServerVersion() // /version on the server
	if err != nil {
		log.Println("GKECluster version error", kc.Name, err)
		return err
	}
	log.Println("GKECluster version", kc.Name, kc.Namespace, kc.KSA, v)

	//sv, err := kc.ServerVersion()
	////CoreV1().ConfigMaps("istio-system").Get(context.Background(), "mesh", metav1.GetOptions{})
	//if err != nil {
	//	return err
	//}
	//
	//log.Println(sv.String())

	//_, err = kc.CoreV1().ConfigMaps("istio-system").List(context.Background(), metav1.ListOptions{})
	//if err != nil {
	//	return err
	//}

	return nil
}

// Use GCP credentials (downloaded SA) or gcloud as trust source.
func TestGCPWithK8SSource(t *testing.T) {

	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()

	// Usual pattern is to use google.DefaultTokenSource - which internally calls this.
	// The order is:
	// - check GOOGLE_APPLICATION_CREDENTIALS - should be downloaded service account, can produce JWTs
	// - ~/.config/gcloud/application_default_credentials.json"
	// - use metadata
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")

	if err != nil {
		t.Skip(err)
	}
	testTS(t, creds.TokenSource)
}

func TestSDK(t *testing.T) {

	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()

	// .config/gcloud/credentials and .config/gcloud/properties
	// The properties file include core/account - the default account to use.
	// credentials include a refresh token and possibly cached access token.
	sdkCfg, err := google.NewSDKConfig("")
	if err != nil {
		t.Skip("No .config/gcloud/credentials")
	}

	// creds.JSON may have additional info.
	// Examples:
	// {
	//   // ???
	//  "client_id": "32555940559.apps.googleusercontent.com",
	//  "client_secret": "",
	//  "refresh_token": "1//...",
	//  "type": "authorized_user"
	//}
	// CredentialsFromJSON can also parse a file.

	ts := sdkCfg.TokenSource(ctx)
	testTS(t, ts)
}

func testTS(t *testing.T, ts oauth2.TokenSource) {
	tt, err := ts.Token()
	if err != nil {
		t.Fatal(err)
	}
	t.Log("Token", tt.AccessToken[0:7])
}
