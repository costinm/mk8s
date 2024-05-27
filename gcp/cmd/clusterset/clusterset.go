package main

import (
	"context"
	"log"

	"github.com/costinm/mk8s/gcp"
)

// Loads kubeconfig and (if available) GKE and fleet clusters.
// Generates a json file using individual 'Dest' and kubeconfigs.
func main() {
	ctx := context.Background()

	gke, err := gcp.New(ctx, nil)
	if err != nil {
		log.Fatal(err)
	}
	gcp.Dump(ctx, gke)
}
