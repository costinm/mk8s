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
	"net/url"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/compute/metadata"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"
)

// Requires real or local metadata server.
// The GCP SA must have k8s api permission.
func TestK8S(t *testing.T) {
	//os.Mkdir("../../out", 0775)
	//os.Chdir("../../out")

	// This or iptables redirect for 169.254.169.254.
	// When running in GKE or GCP - want to use the custom MDS not the platform one.
	// This must be set before metadata package is used - it detects.
	if os.Getenv("GCE_METADATA_HOST") == "" {
		os.Setenv("GCE_METADATA_HOST", "localhost:15014")
	}

	// For the entire test
	ctx, cf := context.WithTimeout(context.Background(), 100*time.Second)
	defer cf()

	kr := &GKE{}

	// If running in GCP, get ProjectId from meta
	kr.DefaultsFromEnvAndMD(ctx)
	if kr.ProjectId == "" {
		if !metadata.OnGCE() {
			t.Skip("Not on GCE and MDS not forwarded")
		}
		t.Skip("Failed to identify project ID")
	}

	// Update the list of clusters.
	cl, err := kr.FindClusters(ctx, "", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) == 0 {
		t.Fatal("No clusters in " + kr.ProjectId)
	}
	if len(cl) == 0 {
		t.Fatal("No clusters in " + kr.ProjectId)
	}

	cl, err = kr.FindClusters(ctx, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) == 0 {
		t.Fatal("No clusters in " + kr.ProjectId)
	}

	err = kr.PickCluster(ctx, cl)
	testCluster := kr.Cluster

	for _, c := range cl {
		if c.ClusterName == "big1" {
			testCluster = c
		}
	}

	rc := testCluster.RestConfig()

	log.Printf("%v\n%v\n", testCluster.restConfig, rc)

	// Run the tests on the first found cluster, unless the test is run with env variables to select a specific
	// location and cluster name.

	t.Run("explicitGKE", func(t *testing.T) {
		kr1 := &GKE{}
		kr1.MeshAddr, _ = url.Parse("gke://" + kr.ProjectId)

		err = kr1.InitGKE(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kr1.Client == nil {
			t.Fatal("No client")
		}

		err = checkClient(kr1.Client)
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("configClusterExplicit", func(t *testing.T) {
		kr1 := &GKE{}
		kr1.MeshAddr, _ = url.Parse(fmt.Sprintf("https://container.googleapis.com/v1/projects/%s/locations/%s/clusters/%s", testCluster.ProjectId, testCluster.ClusterLocation, testCluster.ClusterName))

		err = kr1.InitGKE(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if kr1.Client == nil {
			t.Fatal("No client")
		}

		err = checkClient(kr1.Client)
		if err != nil {
			t.Fatal(err)
		}
	})

}

func checkClient(kc *kubernetes.Clientset) error {
	v, err := kc.ServerVersion() // /version on the server
	if err != nil {
		return err
	}
	log.Println("GKECluster version", v)

	_, err = kc.CoreV1().ConfigMaps("istio-system").Get(context.Background(), "mesh", metav1.GetOptions{})
	if err != nil {
		return err
	}

	//_, err = kc.CoreV1().ConfigMaps("istio-system").List(context.Background(), metav1.ListOptions{})
	//if err != nil {
	//	return err
	//}

	return nil
}
