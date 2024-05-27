package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"

	"github.com/costinm/meshauth/pkg/agent"
	"github.com/costinm/meshauth/util"
	k8sc "github.com/costinm/mk8s/k8s"
	"github.com/costinm/utel"
	uotel "github.com/costinm/utel/otel"
	"go.opentelemetry.io/otel"
)

// Meshauth-agent  can run on a local dev machine, in a docker container or in K8S deamonet set
// or sidecar. Will emulate an MDS server to provide tokens and meta.
//
// TODO: It also handles ext_authz http protocol from Envoy.
// TODO: maintain k8s-like JWT and cert to emulate in-cluster
//
// Source of auth:
// - kube config with token (minimal deps) or in-cluster - for running in K8S
//
// - TODO: MDS in a VM/serverless - with permissions to the cluster
//
//	Non-configurable port 15014 - iptables should redirect port 80 of the MDS.
//
// iptables -t nat -A OUTPUT -p tcp -m tcp -d 169.254.169.254 --dport 80 -j REDIRECT --to-ports 15014
//
// For envoy and c++ grpc - requires /etc/hosts or resolver for metadata.google.internal.
// 169.254.169.254 metadata.google.internal.
//
// Alternative: use ssh-mesh or equivalent to forward to real MDS.
func main() {
	ctx := context.Background()
	maCfg := &agent.Config{}
	// Lookup config file, init basic main file.
	util.MainStart("mds", maCfg)

	// Agent using the minimal telemetry based on slog.
	slog.SetDefault(slog.New(utel.InitDefaultHandler(nil)))

	// Experimental: alternative implementation of Otel API without SDK.
	uotel.InitTracing()
	uotel.InitExpvarMetrics()

	// name ends up as "InstrumentataionLibrary.start
	traceStart := otel.Tracer("xmds-start")

	// Trace startup
	ctx, spanStart := traceStart.Start(ctx, "sync")

	// Init K8S - the agent is using a GKE kube config as bootstrap
	// Namespace will be defaulted from config
	k, err := k8sc.New(ctx, &k8sc.K8SConfig{
		Namespace: maCfg.Namespace,
		KSA:       maCfg.Name,
	})
	if err != nil || k.Default == nil {
		log.Fatal(err)
	}
	p, _, _ := k.Default.GcpInfo()
	if maCfg.ProjectID == "" {
		maCfg.ProjectID = p
	}

	if maCfg.MainMux == nil {
		maCfg.MainMux = http.NewServeMux()
	}
	_, err = agent.SetupAgent(ctx, &maCfg.MeshCfg, k.Default, maCfg.MainMux)
	if err != nil {
		log.Fatal(err)
	}

	spanStart.End()

	go func() {
		// Old Istio Mixer port (for authz)
		err := http.ListenAndServe("0.0.0.0:15014", maCfg.MainMux)
		if err != nil {
			log.Fatal(err)
		}
	}()
	util.MainEnd()
}
