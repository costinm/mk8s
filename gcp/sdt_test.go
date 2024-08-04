package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/logging/logadmin"

	"cloud.google.com/go/logging"
	"cloud.google.com/go/logging/apiv2/loggingpb"
	log "github.com/sirupsen/logrus"
	"google.golang.org/api/iterator"
)

func TestSD(t *testing.T) {
	projectID := os.Getenv("PROJECT_ID")
	if projectID == "" {
		projectID = "dmeshgate"
	}
	ts := time.Now()
	sd, err := NewStackdriver(projectID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	txt := "TEST_PROPAGATION " + time.Now().String()

	ch := make(chan *loggingpb.LogEntry)

	//if false {
	//	sd.LogAdmin.CreateSink(ctx, &logadmin.Sink{})
	//}
	//si := sd.LogAdmin.Sinks(ctx)
	//for {
	//	s, err := si.Next()
	//	if err != nil {
	//		break
	//	}
	//	slog.Info("sink", "s", s)
	//}
	// Buckets: _Default and _Required
	// gcloud logging buckets create BUCKET_ID --location=LOCATION --enable-analytics --async OPTIONAL_FLAGS

	// ln := "projects/" + projectID + "/logs/stdout"
	l := sd.Logging.Logger("test")

	// can't set log name
	err = l.LogSync(ctx, logging.Entry{Payload: "Start test"})
	if err != nil {
		t.Error(err)
	}

	tailCtx, cf := context.WithCancel(ctx)
	conter := atomic.Int64{}
	sd.LogTail(tailCtx, "", func(entries []*loggingpb.LogEntry) {
		if entries == nil {
			ch <- nil
			return
		}
		// Batch of entries for efficiency
		for _, e := range entries {
			conter.Add(1)
			if strings.Contains(e.GetTextPayload(), "TEST_PROPAGATION") {
				//slog.Info(e.GetTextPayload(), "e", e)
				ch <- e
				return
			}
		}
	})

	// gcloud logging client:
	// - detect resource - similar to otel
	// - options for DelayThreshold ( 1 sec )
	// - options for BundleEntryCountThreshold (1000), BundleBytesThr (8M),
	//   BufferedByteLimit based on the quotas.
	// - BufferedByteLimit = 1G
	// - writeTimeout = 10 min !

	t0 := time.Now()
	// That means: 1 sec until this is sent, plus more until we get it back.

	l.Log(logging.Entry{Payload: txt})

	l.LogSync(ctx, logging.Entry{Payload: txt})

	select {
	case le := <-ch:
		slog.Info("Got log via tail", "t", time.Since(t0), "t1", t0.Sub(ts), "e", le, "cnt", conter.Load())
	case <-time.After(10 * time.Second):
		fmt.Println("timeout ", conter.Load())
	}

	cf()

	// Poll method.
	// sd.Logs(ctx, "stdout")

	bc := 0
	rc := 0
	t0 = time.Now()

	// Filters
	//   logName = projects/MYPROJECT/logs/MYLOG
	//

	ei := sd.LogAdmin.Entries(ctx, logadmin.Filter("TEST_PROPAGATION"), logadmin.NewestFirst(), logadmin.ProjectIDs([]string{projectID}))
	for {
		n, err := ei.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Error(err)
			break
		}
		nb, err := json.Marshal(n)

		rc++
		bc += len(nb)
		if strings.Contains(fmt.Sprintf("%v", n.Payload), "TEST_PROPAGATION") {
			slog.Info("payload", "e", n)
			break
		}
		if rc == 1000 {
			break
		}
		//pi := ei.PageInfo()
		//pi.MaxSize
		//pi.Token
	}

	slog.Info("Got entries", "n", rc, "bc", bc, "t", time.Since(t0))
}
