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
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/golang/protobuf/ptypes/duration"
	"google.golang.org/api/monitoring/v3"

	//"google.golang.org/api/monitoring/v1"
	//monitoring "google.golang.org/genproto/googleapis/monitoring/v3"

	"cloud.google.com/go/logging"

	loggingv2 "cloud.google.com/go/logging/apiv2"
	"cloud.google.com/go/logging/apiv2/loggingpb"
	"cloud.google.com/go/logging/logadmin"

	googlepb "github.com/golang/protobuf/ptypes/timestamp"

	"google.golang.org/api/iterator"

	monitoringv3 "cloud.google.com/go/monitoring/apiv3/v2"
	"cloud.google.com/go/monitoring/apiv3/v2/monitoringpb"

	//"google.golang.org/api/monitoring/v3"

	metricpb "google.golang.org/genproto/googleapis/api/metric"
	monitoredrespb "google.golang.org/genproto/googleapis/api/monitoredres"
)

// # Direct integration with GCP stackdriver
//
//Data ingestion is expected to be based on collectors, OTLP and native instrumentations.
//
//This package handles accessing the data using the GCP native interfaces - primarily
//for query, scripting and testing - but also for import/export of data.
//
//## Batching and quotas
//
//Like all APIs, GCP telemetry has different quotas: for example max 10 'watchers' on
//logs. This is needed because some operations can be very expensive and may be miss-used.
//
//For tests and automation it is best to batch requests - it can also be far more
//efficient as some ops can be optimized. There are limits on the size of the batches too.
//
//## Rest and gRPC
//
//Almost all operations can be invoked with 'curl', json and an access token.
//
//Few - like log tail - can't. The gRPC interface is also more efficient and clear - exposes the actual protos instead of abstracting away and obfuscating.

// Original library used:
// 	"google.golang.org/api/monitoring/v3" - deprecated, generated library using json encoding
//	and "google.golang.org/api/transport/http", "google.golang.org/api/googleapi" clients.
//
// Alternatives to try: generic swagger library.
//
// Recommended: https://pkg.go.dev/cloud.google.com/go
//

// GCP-related adjustments for logging.
// This doesn't have deps on google libraries and is not specific - may be used by other stores.

// https://github.com/imjasonh/gcpslog/blob/main/setup.go
// https://cloud.google.com/logging/docs/agent/logging/configuration#special-fields
// https://cloud.google.com/logging/docs/reference/v2/rest/v2/LogEntry

// Google logging has the structure /projects/PROJECT_ID/logs/TAG
// TAG is sent as logName.
//
// For example: stdout for the collected logs.
//
// 'resource' is required and constant for the generated logs - they should be added by a post-processor/collector,
// no need to log on each entry
//
// The resource will have a type (k8s_container) and labels - project_id, pod_name, namespace_name,
// location, cluster_name, container_name.
//
// Labes on the log are set by the collector or intermediaries:
// - compute.googleapis.com/resource_name - the VM (node)
// - k8s-pod/app - the deployment  and other labels on the pod
//
// Resource, labels are filled in by the collector so don't need to be included.

// Audit logs - have tag like cloudaudit.googleapis.com%2Factivity
//
// An important class of logs are 'audit' - they should have priority and have consistent info
// type.googleapis.com/google.cloud.audit.AuditLog type includes
// authenticationInfo.principalEmail
// authorizationInfo[].permission and resource
// methodname
// requestMetadata.callerIp
// status.code

// Metrics
// - delta metrics, one per minute ( if not zero I assume )
// - counter and distribution
// - have a label named "log" with the logName field
// - Name: User-defined: logging.googleapis.com/user/USER_METRIC_NAME
// -

// Istio reports k8s_container metrics, which get aggregated to istio_canonical_service:
//      canonical_service_name, revision, mesh_uid: from the labels

var (
	// https://istio.io/latest/docs/reference/config/metrics/
	// https://cloud.google.com/monitoring/api/metrics_istio

	// Options for dependencies:
	// google.golang.org/api - all APIs, generated, depend on
	//    google.golang.org/api/googleapi, internal, option, transport/http
	//    grpc, protobuf, oauth2, opencensus
	//
	// googleapis/googleapis repo - protos
	//
	// https://github.com/googleapis/google-cloud-go - or cloud.google.com/go - high level
	// - also subdirs like monitoring/ with go.mod
	// - using protoc-gen-go_gapic
	//
	// github.com/googleapis/go-genproto - the generated protos, deprecated - moving to gapic - alias things like
	// cloud.google.com/go/iam/apiv2/iampb ->
	// or google.golang.org/genproto
	//   - depend on cloud.google.com/go/xxxx
	//
	// https://buf.build/googleapis/googleapis - the core types used in other projects ( not including google api)
	//

	// Note on auth: may use scopes:	"https://www.googleapis.com/auth/monitoring",
	//		"https://www.googleapis.com/auth/monitoring.read",
	//		"https://www.googleapis.com/auth/monitoring.write"

	// service/client
	IstioPrefix       = "istio.io/service/client/"
	IstioPrefixServer = "istio.io/service/server/"

	// Resource:
	// - k8s_pod
	// - gce_instance

	// - istio_canonical_service - for request_count, roundtrip_latencies

	// "Sampled every 60 sec, not visible up to 180 sec" - so ~4 min window
	// for a test to validate metrics after request is made, or for autoscale to adjust
	IstioMetrics = []string{
		// Autoscaling for short lived
		"request_count", // DELTA, INT64, 1

		// May be used for auto-scaling if it gets too high, only for
		// short-lived only.
		"roundtrip_latencies", // DELTA, DISTRIBUTION

		// Autoscaling for WS/long lived.
		"connection_close_count", // CUMULATIVE, INT64, 1
		"connection_open_count",  // CUMULATIVE, INT64, 1

		// Useful for bandwidth limits - if the rate is close to line speed.
		// Very unlikely in this form.
		"received_bytes_count", // CUMULATIVE, INT64, 1
		"sent_bytes_count",     // CUMULATIVE, INT64, 1

		// Useful for stats on payload size, not for scaling or health.
		"request_bytes",  // DELTA, DISTRIBUTION
		"response_bytes", // DELTA, DISTRIBUTION
	}

	IstioLabels = []string{
		"request_protocol",                        // Protocol of the request or connection (e.g. HTTP, gRPC, TCP).
		"service_authentication_policy",           // : Determines if Istio was used to secure communications between services and how. Currently supported values: "NONE", "MUTUAL_TLS".
		"mesh_uid",                                // : Unique identifier for the mesh that is being monitored.
		"destination_service_name",                //: Name of destination service.
		"destination_service_namespace",           //: Namespace of destination service.
		"destination_port",                        //: (INT64) Port of the destination service.
		"source_principal",                        //: Principal of the source workload instance.
		"source_workload_name",                    //: Name of the source workload.
		"source_workload_namespace",               //: Namespace of the source workload.
		"source_owner",                            //: Owner of the source workload instance (e.g. k8s Deployment).
		"destination_principal",                   //: Principal of the destination workload instance.
		"destination_workload_name",               //: Name of the destination workload.
		"destination_workload_namespace",          //: Namespace of the destination workload.
		"destination_owner",                       //: Owner of the destination workload instance (e.g. k8s Deployment).
		"source_canonical_service_name",           //: The canonical service name of the source workload instance.
		"destination_canonical_service_name",      //: The canonical service name of the destination workload instance.
		"source_canonical_service_namespace",      //: The canonical service namespace of the source workload instance.
		"destination_canonical_service_namespace", //: The canonical service namespace of the destination workload instance.
		"source_canonical_revision",               //: The canonical service revision of the source workload instance.
		"destination_canonical_revision",          //: The canonical service revision of the destination workload instance.

		// For *_bytes, request_count,
		// Not for connection*, *_bytes_count
		"request_operation", // : Unique string used to identify the API method (if available) or HTTP Method.
		"api_version",       // : Version of the API.
		"response_code",     // : (INT64) Response code of the request according to protocol.
		"api_name",          // : Name of the API.
	}

	VMResourceLabels = []string{
		"project_id",
		"instance_id", // for gce_instance
		"zone",
	}
	PodResourceLabels = []string{
		"project_id",
		"location", // for pod
		"cluster_name",
		"namespace_name",
		"pod_name",
	}

	// This is aggregated from pod metrics.
	SvcResourceLabels = []string{
		"mesh_uid",
		"project_id",
		"location",
		"namespace_name",
		"canonical_service_name",
		"revision",
	}

	// Typical metrics:
	// destination_canonical_revision:latest
	// destination_canonical_service_name:fortio-cr
	// destination_canonical_service_namespace:fortio
	// destination_owner:unknown
	// destination_port:15442
	// destination_principal:spiffe://wlhe-cr.svc.id.goog/ns/fortio/sa/default
	// destination_service_name:fortio-cr-icq63pqnqq-uc
	// destination_service_namespace:fortio
	// destination_workload_name:fortio-cr-sni
	// destination_workload_namespace:fortio
	// mesh_uid:proj-601426346923
	// request_operation:GET
	// request_protocol:http
	// response_code:200
	// service_authentication_policy:unknown
	// source_canonical_revision:v1
	// source_canonical_service_name:fortio
	// source_canonical_service_namespace:fortio
	// source_owner:kubernetes://apis/apps/v1/namespaces/fortio/deployments/fortio
	// source_principal:spiffe://wlhe-cr.svc.id.goog/ns/fortio/sa/default
	// source_workload_name:fortio
	// source_workload_namespace:fortio

)

// WIP.
//
// Integration and testing with stackdriver for 'proxyless' modes.
//
// With Envoy, this is implemented using WASM or native filters.
//
// For proxyless (gRPC or generic hbone / uProxy) we need to:
// - decode and generate the Istio header containing client info
// - generate the expected istio metrics.
//

// Request can also use the REST API:
// monitoring.googleapis.com/v3/projects/NAME/timeSeries
//   ?aggregation.alignmentPeriod=60s
//   &aggregation.crossSeriesReducer=REDUCE_NONE
//   &aggregation.perSeriesAligner=ALIGN_RATE
//   &alt=json
//   &filter=metrics.type+%3D+%22istio.io%2Fservice%2Fclient%2Frequest_count%22+AND+resource.type+%3D+%22istio_canonical_service%22+AND+resource.labels.namespace_name+%3D+%22fortio%22
//  &interval.endTime=2021-09-30T14%3A32%3A51-07%3A00
//  &interval.startTime=2021-09-30T14%3A27%3A51-07%3A00
//  &prettyPrint=false

type Stackdriver struct {
	projectID string
	// Deprecated
	monitoringService *monitoring.Service
	MetricClient      *monitoringv3.MetricClient

	// Old style - with batching, etc
	Logging *logging.Client

	// gRPC based, raw, supports tail
	LoggingV2 *loggingv2.Client

	// Log admin - list, etc
	LogAdmin *logadmin.Client
}

var (
	queryInterval = -30 * time.Minute
)

// Resource can be mapped to a FQDN representing the source of the log.
// In most cases it can be represented as the equivalent string, for extra clarity.
type Resource struct {
	// k8s_cluster
	Type string

	// Example: cluster_name, location, projct_id
	Labels map[string]string
}

func NewStackdriver(projectID string) (*Stackdriver, error) {
	ctx := context.Background()
	// Deprecated
	monitoringService, err := monitoring.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitoring service: %v", err)
	}

	client, err := monitoringv3.NewMetricClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get monitoring service: %v", err)
	}

	// Creates a client.
	lclient, err := logging.NewClient(ctx, projectID)
	if err != nil {
		return nil, err
	}

	// See cloud.google.com/go/logging docs.
	// - logadmin client - configurations
	//
	laclient, err := logadmin.NewClient(ctx, projectID)
	if err != nil {
		return nil, err
	}

	l2client, err := loggingv2.NewClient(ctx)
	if err != nil {
		return nil, err
	}

	// TODO: init otel SDK with GCP exporters !

	return &Stackdriver{projectID: projectID, monitoringService: monitoringService, MetricClient: client,
		Logging: lclient, LoggingV2: l2client,
		LogAdmin: laclient}, nil
}

func (s *Stackdriver) NewLogSink(name, topic string) error {

	return nil
}

// 120,000 per minute,  each log batch can be 10M. 1000 resources/batch
// Expect OTel library to batch when uploading. Not a good idea to log individual
// entries in a script.
//
// For 'stdout' log, it is better to just print to stdout and let the collector batch.

// Metrics: 10 labels/metric

// Traces: 300/min for get/list, each get returns 1000, 32 labels/span

// LogTail is an efficient way to export log data for local processing.
//
// It has a low quotas 10 per project !!! -  but can do 60K entries per minute.
// Alternative: export to pubsub, which can do 1GB/s.
func (s *Stackdriver) LogTail(ctx context.Context, f string, cb func([]*loggingpb.LogEntry)) error {
	client := s.LoggingV2

	stream, err := client.TailLogEntries(ctx)
	if err != nil {
		return err
	}

	req := &loggingpb.TailLogEntriesRequest{
		ResourceNames: []string{
			// may also be a project/location/bucket/view
			"projects/" + s.projectID,
		},
		Filter:       f,
		BufferWindow: &duration.Duration{Nanos: 1},
	}
	if err := stream.Send(req); err != nil {
		return fmt.Errorf("stream.Send error: %w", err)
	}

	// TODO: batch size, min/max timestamp.
	// save the ranges to file - and use Log list to get the gaps.

	// read and print two or more streamed log entries
	go func() {
		defer stream.CloseSend()
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				//log.Println("CANCELED")
				cb(nil)
				return
				//return fmt.Errorf("stream.Recv error: %w", err)
			}

			cb(resp.Entries)

		}
	}()
	return nil
}

func (s *Stackdriver) LogList(ctx context.Context) ([]string, error) {
	iter := s.LogAdmin.Logs(ctx)
	logs := []string{}
	for {
		log, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return logs, err
		}
		logs = append(logs, log)
	}
	return logs, nil
}

// 60/min
func (s *Stackdriver) Logs(ctx context.Context, logName string) ([]*logging.Entry, error) {
	// Selects the log to write to.
	var entries []*logging.Entry
	lastHour := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)

	iter := s.LogAdmin.Entries(ctx,
		// Only get entries from the "log-example" log within the last hour.

		logadmin.Filter(fmt.Sprintf(`logName = "projects/%s/logs/%s" AND timestamp > "%s"`, s.projectID, url.PathEscape(logName), lastHour)),
		// Get most recent entries first.
		logadmin.NewestFirst(),
	)

	// Fetch the most recent 20 entries.
	for len(entries) < 20 {
		entry, err := iter.Next()
		if err == iterator.Done {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// Examples:
// projects/costin-istio3/metricDescriptors/...
//
// networkservices.googleapis.com/https/request_count
// loadbalancing.googleapis.com/https/request_count
//
// Explorer can be used to see resources and queries that are active.
// And to query - ex:
//
//	sum(rate(monitoring_googleapis_com:billing_samples_ingested{monitored_resource="global"}[${__interval}]))
func (s *Stackdriver) ListMetrics(ctx context.Context) ([]*monitoring.MetricDescriptor, error) {
	monitoringService, err := monitoring.NewService(context.Background())
	if err != nil {
		return nil, err
	}
	lr := monitoringService.Projects.MetricDescriptors.List("projects/" + s.projectID).Context(ctx)
	resp, err := lr.Do()
	if err != nil {
		return nil, err
	}
	if resp.HTTPStatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get expected status code from monitoring service, got: %d", resp.HTTPStatusCode)
	}
	return resp.MetricDescriptors, nil
}

// Get metrics.
//
// Filter options:
// project, group.id, resource.type, resource.labels.[KEY], metrics.type,
// metrics.labels.[KEY]
func (s *Stackdriver) ListTimeSeries(ctx context.Context, namespace, resourceType, metricName, extra string, startTime time.Time, endTime time.Time) ([]*monitoring.TimeSeries, error) {

	f := fmt.Sprintf("metric.type = %q ",
		metricName)
	if resourceType != "" {
		f = f + fmt.Sprintf(" AND resource.type = %q", resourceType)
	}
	if namespace != "" {
		f = f + fmt.Sprintf(" AND resource.labels.namespace_name = %q", namespace)
	}
	if extra != "" {
		f = f + extra
	}

	//s.MetricClient.ListTimeSeries(ctx, &monitoringpb.ListTimeSeriesRequest{
	//	Filter: f,
	//	Aggregation: &monitoringpb.Aggregation{},
	//	Interval: &monitoringpb.TimeInterval{},
	//	Name: "",
	//})
	lr := s.monitoringService.Projects.TimeSeries.List(fmt.Sprintf("projects/%v", s.projectID)).
		IntervalStartTime(startTime.Format(time.RFC3339)).
		IntervalEndTime(endTime.Format(time.RFC3339)).
		AggregationCrossSeriesReducer("REDUCE_NONE").
		AggregationAlignmentPeriod("60s").
		AggregationPerSeriesAligner("ALIGN_RATE").
		Filter(f).
		Context(ctx)
	resp, err := lr.Do()
	if err != nil {
		return nil, err
	}
	if resp.HTTPStatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get expected status code from monitoring service, got: %d", resp.HTTPStatusCode)
	}

	return resp.TimeSeries, nil
}

func (s *Stackdriver) Close() {
	_ = s.MetricClient.Close()
	_ = s.Logging.Close()
}

// metricName: full, like istio.io/service/server/request_count
func (s *Stackdriver) CreateTimeSeries(ctx context.Context, resourceType, metricName string, val float64) error {

	// Prepares an individual data point
	// For 'gauge' - start is optional
	// For count - delta must be used and start is needed
	// TODO: Dist value, string values
	dataPoint := &monitoringpb.Point{
		Interval: &monitoringpb.TimeInterval{
			EndTime: &googlepb.Timestamp{
				Seconds: time.Now().Unix(),
			},
			StartTime: &googlepb.Timestamp{
				Seconds: time.Now().Unix() - 10,
			},
		},
		Value: &monitoringpb.TypedValue{
			Value: &monitoringpb.TypedValue_DoubleValue{
				DoubleValue: val,
			},
		},
	}

	// Writes time series data.
	if err := s.MetricClient.CreateTimeSeries(ctx, &monitoringpb.CreateTimeSeriesRequest{
		Name: fmt.Sprintf("projects/%s", s.projectID),
		TimeSeries: []*monitoringpb.TimeSeries{
			{
				Metric: &metricpb.Metric{
					Type: metricName,
					Labels: map[string]string{
						"store_id": "Pittsburg",
					},
				},
				Resource: &monitoredrespb.MonitoredResource{
					Type: "global",
					Labels: map[string]string{
						"project_id": s.projectID,
					},
				},
				Points: []*monitoringpb.Point{
					dataPoint,
				},
			},
		},
	}); err != nil {
		return err
	}

	return nil
}

// For a metrics, list resource types that generated the metrics and the names.
func (s *Stackdriver) ListResources(ctx context.Context, namespace, metricName, extra string) ([]*monitoring.TimeSeries, error) {
	endTime := time.Now()
	startTime := endTime.Add(queryInterval)

	f := fmt.Sprintf("metrics.type = %q ", metricName)
	if namespace != "" {
		f = f + fmt.Sprintf(" AND resource.labels.namespace_name = %q", namespace)
	}
	if extra != "" {
		f = f + extra
	}

	lr := s.monitoringService.Projects.TimeSeries.List(fmt.Sprintf("projects/%v", s.projectID)).
		IntervalStartTime(startTime.Format(time.RFC3339)).
		IntervalEndTime(endTime.Format(time.RFC3339)).
		AggregationCrossSeriesReducer("REDUCE_NONE").
		AggregationAlignmentPeriod("60s").
		AggregationPerSeriesAligner("ALIGN_RATE").
		Filter(f). //, destCanonical
		Context(ctx)
	resp, err := lr.Do()
	if err != nil {
		return nil, err
	}
	if resp.HTTPStatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get expected status code from monitoring service, got: %d", resp.HTTPStatusCode)
	}

	return resp.TimeSeries, nil
}

type SDListReq struct {
	ProjectID string

	Namespace string

	// Example: istio.io/service/client/request_count
	Metric    string

	ExtraQuery string

	// Ex: istio_canonical_service
	ResourceType string

	Json               bool
	// Include metrics with zero value
	IncludeZeroMetrics bool
}

func SDList(r SDListReq) {

	sd, err := NewStackdriver(r.ProjectID)
	if err != nil {
		panic(err)
	}

	rs, err := sd.ListResources(context.Background(),
		r.Namespace,
		r.Metric, r.ExtraQuery)

	log.Println(rs, err)
	// Verify client side metrics (in pod) reflect the CloudrRun server properties
	ts, err := sd.ListTimeSeries(context.Background(),
		r.Namespace, r.ResourceType,
		r.Metric, r.ExtraQuery, time.Now().Add(-1 * time.Hour), time.Now())

	//" AND metrics.labels.source_canonical_service_name = \"fortio\"" +
	//		" AND metrics.labels.response_code = \"200\"")
	if err != nil {
		log.Fatalf("Error %v", err)
	}

	for _, tsr := range ts {
		v := tsr.Points[0].Value
		if !r.IncludeZeroMetrics && *v.DoubleValue == 0 {
			continue
		}
		if r.Json {
			d, _ := json.Marshal(tsr)
			fmt.Println(string(d))
		} else {
			fmt.Printf("%v %v\n", *v.DoubleValue, tsr.Metric.Labels)
		}
	}
}
