package gcp

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"strings"
	"time"

	"cloud.google.com/go/logging/apiv2/loggingpb"
)

var (
	val    = flag.Float64("val", 1, "Value to add or set")

	mtype = flag.String("type", "counter", "Type of the metric - counter, gauge. TODO: Histogram")

	verbose = flag.Bool("v", false, "Verbose")
	res     = flag.String("res", "", "Name of the resource")
	watch   = flag.Bool("w", false, "Watch")


	extra = flag.String("x", "", "Extra query parameters")

)

//var (
//	queryInterval = -5 * time.Minute
//)

type TelemetryBatch struct {
}

type MetricListRequest struct {
	ProjectID string
	Name      string
	Filter    string
	Verbose   bool

	// Name of the resource to create
	Res string
}

type MetricUpdateRequest struct {
	ProjectID string
	// Name of the metric to create
	Metric string
	Val    float64
	// Name of the resource to create
	Res string
}

func MetricUpdate(ctx context.Context, r *MetricUpdateRequest) error  {
	sd, err := NewStackdriver(r.ProjectID)
	if err != nil {
		return err
	}
	defer sd.Close()


	err = sd.CreateTimeSeries(ctx, r.Res, r.Metric, r.Val)
	if err != nil {
		return err
	}
	return nil
}
// Metrics tool. Can:
// - list metrics (top level, equivalent to logName or a database or 'file').
// - add metrics
// - query
//
// First param must be metric:[NAME] or log:[NAME]
// Args determine what to do.
// TODO: Stdin can be used for injecting data.
func TelemetryList(ctx context.Context, r *MetricListRequest) error {
	if r.Name == "" {
		fmt.Println("Requires: metric:[NAME] or log:[NAME]")
		return nil
	}
	var err error
	var sd *Stackdriver
	if r.ProjectID == "" {
		return errors.New("Requires projectID")
	}
	sd, err = NewStackdriver(r.ProjectID)
	if err != nil {
		return err
	}
	defer sd.Close()


	/*	if len(*res) > 0 {
			rb, err := ioutil.ReadFile(*res + ".json")
			if err != nil {
				log.Panic(err)
			}
			var r resource.Resource
		}
	*/
	a0 := strings.Split(r.Name, ":")

	if a0[0] == "l" || a0[0] == "log" {
		if *watch {
			sd.LogTail(ctx, r.Filter, func(le []*loggingpb.LogEntry) {
				for _, lle := range le {
					fmt.Println(lle)
					fmt.Println()
				}
			})
			return nil
		}

		if len(a0) == 1 {
			// List
			ll, err := sd.LogList(ctx)
			if err != nil {
				log.Fatal(err)
			}
			for _, v := range ll {
				fmt.Println(v)
			}
			return nil
		}

		// List logs given a logName
		l := a0[1]
		le, err := sd.Logs(ctx, l)
		if err != nil {
			log.Panic(err)
		}
		for _, v := range le {
			fmt.Println(v)
			fmt.Println()
		}
		return nil
	}

	if a0[0] == "m" || a0[0] == "metric" {

		if r.ProjectID != "" {
			if len(a0) == 1 {
				ml, err := sd.ListMetrics(ctx)
				if err != nil {
					log.Panic(err)
				}
				for _, v := range ml {
					if r.Verbose {
						vb, _ := v.MarshalJSON()
						fmt.Println(string(vb))
					} else {
						// Name is projects/PROJECT_ID/metricDescriptor/TYPE/DISPLAY_NAME
						// MetricReader query flattens it.
						l := []string{}
						for _, vv := range v.Labels {
							l = append(l, vv.Key)
						}
						fmt.Println(v.Type, v.MetricKind, v.ValueType, l)
					}
				}
				return nil
			}
			m := a0[1]

			if r.Res == "" {

				timeSeries, err := sd.ListTimeSeries(ctx, "", "", m, *extra, time.Now().Add(-6*time.Hour), time.Now())
				//timeSeries, err := listTS(ctx, startTime, endTime, *metric)
				if err != nil {
					log.Panic(err)
				}
				fmt.Printf("metrics: #%d\n", len(timeSeries))
				for _, timeSery := range timeSeries {
					fmt.Println("  - kind: " + timeSery.MetricKind + " # " + timeSery.ValueType)
					fmt.Println("    name: " + timeSery.Metric.Type)
					if *verbose {
						r, _ := timeSery.Resource.MarshalJSON()
						fmt.Println(string(r))
						r, _ = timeSery.Metric.MarshalJSON()
						fmt.Println(string(r))
						for _, p := range timeSery.Points {
							r, _ := p.MarshalJSON()
							fmt.Println(string(r))
						}
						fmt.Println("---\n")
					} else {
						fmt.Println("    res: " + timeSery.Resource.Type)
						fmt.Println("    resLabels:")
						for k, v := range timeSery.Resource.Labels {
							fmt.Println("      " + k + ": " + v)
						}
						fmt.Printf("    labels: #%d\n", len(timeSery.Metric.Labels))
						for k, v := range timeSery.Metric.Labels {
							fmt.Println("      " + k + ": " + v)
						}
						fmt.Printf("    points: #%d\n", len(timeSery.Points))
						for _, p := range timeSery.Points {
							fmt.Printf("      - %.2f\n", *p.Value.DoubleValue)
						}
					}
					fmt.Println("---\n")
				}
				fmt.Println("---\n")
				return nil
			}
		return nil
		}


		// TODO: query from Prom as well.

	}

	return nil
}
