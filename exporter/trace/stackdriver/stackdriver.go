// Copyright 2017, OpenCensus Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package stackdriver contains an exporter for Stackdriver Trace.
//
// Example:
//
// 	import "go.opencensus.io/trace/adaptor/stackdriver"
//
// 	exporter, err := stackdriver.NewExporter(stackdriver.Options{ProjectID: *project})
// 	if err != nil {
// 		log.Println(err)
// 	} else {
// 		trace.RegisterExporter(exporter)
// 	}
//
// The package uses Application Default Credentials to authenticate.  See
// https://developers.google.com/identity/protocols/application-default-credentials
package stackdriver

import (
	"fmt"
	"log"
	"sync"
	"time"

	"go.opencensus.io/internal"

	tracingclient "cloud.google.com/go/trace/apiv2"
	"go.opencensus.io/trace"
	"golang.org/x/net/context"
	"google.golang.org/api/option"
	"google.golang.org/api/support/bundler"
	tracepb "google.golang.org/genproto/googleapis/devtools/cloudtrace/v2"
)

// Exporter is an implementation of trace.Exporter that uploads spans to
// Stackdriver.
type Exporter struct {
	projectID string
	bundler   *bundler.Bundler
	// uploadFn defaults to uploadToStackdriver; it can be replaced for tests.
	uploadFn func(spans []*trace.SpanData)
	overflowLogger
	client *tracingclient.Client
}

var _ trace.Exporter = (*Exporter)(nil)

// Options contains options for configuring an exporter.
//
// Only ProjectID is required.
type Options struct {
	ProjectID string
	// ClientOptions contains options used to configure the Stackdriver client.
	ClientOptions []option.ClientOption
	// BundleDelayThreshold is maximum length of time to wait before uploading a
	// bundle of spans to Stackdriver.
	BundleDelayThreshold time.Duration
	// BundleCountThreshold is the maximum number of spans to upload in one bundle
	// to Stackdriver.
	BundleCountThreshold int
}

// NewExporter returns an implementation of trace.Exporter that uploads spans
// to Stackdriver.
func NewExporter(o Options) (*Exporter, error) {
	co := []option.ClientOption{
		option.WithUserAgent(internal.UserAgent),
		// NB: NewClient below adds WithEndpoint, WithScopes options.
	}
	co = append(co, o.ClientOptions...)
	client, err := tracingclient.NewClient(context.Background(), co...)
	if err != nil {
		return nil, fmt.Errorf("stackdriver: couldn't initialize client: %v", err)
	}
	return newExporter(o, client), nil
}

func newExporter(o Options, client *tracingclient.Client) *Exporter {
	e := &Exporter{
		projectID: o.ProjectID,
		client:    client,
	}
	bundler := bundler.NewBundler((*trace.SpanData)(nil), func(bundle interface{}) {
		e.uploadFn(bundle.([]*trace.SpanData))
	})
	if o.BundleDelayThreshold > 0 {
		bundler.DelayThreshold = o.BundleDelayThreshold
	} else {
		bundler.DelayThreshold = 2 * time.Second
	}
	if o.BundleCountThreshold > 0 {
		bundler.BundleCountThreshold = o.BundleCountThreshold
	} else {
		bundler.BundleCountThreshold = 50
	}
	// The measured "bytes" are not really bytes, see exportReceiver.
	bundler.BundleByteThreshold = bundler.BundleCountThreshold * 200
	bundler.BundleByteLimit = bundler.BundleCountThreshold * 1000
	bundler.BufferedByteLimit = bundler.BundleCountThreshold * 2000

	e.bundler = bundler
	e.uploadFn = e.uploadToStackdriver
	return e
}

// Export exports a SpanData to Stackdriver Trace.
func (e *Exporter) Export(s *trace.SpanData) {
	// n is a length heuristic.
	n := 1
	n += len(s.Attributes)
	n += len(s.Annotations)
	n += len(s.MessageEvents)
	n += len(s.StackTrace)
	err := e.bundler.Add(s, n)
	switch err {
	case nil:
		return
	case bundler.ErrOversizedItem:
		go e.uploadFn([]*trace.SpanData{s})
	case bundler.ErrOverflow:
		e.overflowLogger.log()
	default:
		log.Println("OpenCensus Stackdriver exporter: failed to upload span:", err)
	}
}

// Flush waits for exported trace spans to be uploaded.
//
// This is useful if your program is ending and you do not want to lose recent
// spans.
func (e *Exporter) Flush() {
	e.bundler.Flush()
}

// uploadToStackdriver uploads a set of spans to Stackdriver.
func (e *Exporter) uploadToStackdriver(spans []*trace.SpanData) {
	req := tracepb.BatchWriteSpansRequest{
		Name:  "projects/" + e.projectID,
		Spans: make([]*tracepb.Span, 0, len(spans)),
	}
	for _, span := range spans {
		req.Spans = append(req.Spans, protoFromSpanData(span, e.projectID))
	}
	err := e.client.BatchWriteSpans(context.Background(), &req)
	if err != nil {
		log.Printf("OpenCensus Stackdriver exporter: failed to upload %d spans: %v", len(spans), err)
	}
}

// overflowLogger ensures that at most one overflow error log message is
// written every 5 seconds.
type overflowLogger struct {
	mu    sync.Mutex
	pause bool
	accum int
}

func (o *overflowLogger) delay() {
	o.pause = true
	time.AfterFunc(5*time.Second, func() {
		o.mu.Lock()
		defer o.mu.Unlock()
		switch {
		case o.accum == 0:
			o.pause = false
		case o.accum == 1:
			log.Println("OpenCensus Stackdriver exporter: failed to upload span: buffer full")
			o.accum = 0
			o.delay()
		default:
			log.Printf("OpenCensus Stackdriver exporter: failed to upload %d spans: buffer full", o.accum)
			o.accum = 0
			o.delay()
		}
	})
}

func (o *overflowLogger) log() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.pause {
		log.Println("OpenCensus Stackdriver exporter: failed to upload span: buffer full")
		o.delay()
	} else {
		o.accum++
	}
}
