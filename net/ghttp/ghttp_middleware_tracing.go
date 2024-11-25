// Copyright GoFrame Author(https://goframe.org). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://github.com/gogf/gf.

package ghttp

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/gogf/gf/v2"
	"github.com/gogf/gf/v2/internal/httputil"
	"github.com/gogf/gf/v2/internal/tracing"
	"github.com/gogf/gf/v2/internal/utils"
	"github.com/gogf/gf/v2/net/gtrace"
	"github.com/gogf/gf/v2/os/gctx"
	"github.com/gogf/gf/v2/text/gstr"
	"github.com/gogf/gf/v2/util/gconv"
)

const (
	tracingInstrumentName                       = "github.com/gogf/gf/v2/net/ghttp.Server"
	tracingEventHttpRequest                     = "http.request"
	tracingEventHttpRequestHeaders              = "http.request.headers"
	tracingEventHttpRequestBaggage              = "http.request.baggage"
	tracingEventHttpRequestBody                 = "http.request.body"
	tracingEventHttpResponse                    = "http.response"
	tracingEventHttpResponseHeaders             = "http.response.headers"
	tracingEventHttpResponseBody                = "http.response.body"
	tracingMiddlewareHandled        gctx.StrKey = `MiddlewareServerTracingHandled`
)

// internalMiddlewareServerTracing is a serer middleware that enables tracing feature using standards of OpenTelemetry.
func internalMiddlewareServerTracing(r *Request) {
	var (
		ctx = r.Context()
	)
	// Mark this request is handled by server tracing middleware,
	// to avoid repeated handling by the same middleware.
	if ctx.Value(tracingMiddlewareHandled) != nil {
		r.Middleware.Next()
		return
	}

	ctx = context.WithValue(ctx, tracingMiddlewareHandled, 1)
	var (
		span trace.Span
		tr   = otel.GetTracerProvider().Tracer(
			tracingInstrumentName,
			trace.WithInstrumentationVersion(gf.VERSION),
		)
	)
	ctx, span = tr.Start(
		getSpanContext(ctx, r.Header),
		r.URL.String(),
		trace.WithSpanKind(trace.SpanKindServer),
	)
	defer span.End()

	span.SetAttributes(gtrace.CommonLabels()...)

	// Inject tracing context.
	r.SetCtx(ctx)

	// If it is now using a default trace provider, it then does no complex tracing jobs.
	if gtrace.IsUsingDefaultProvider() {
		r.Middleware.Next()
		return
	}

	reqAttrs := []attribute.KeyValue{
		attribute.String(tracingEventHttpRequestHeaders, gconv.String(httputil.HeaderToMap(r.Header))),
		attribute.String(tracingEventHttpRequestBaggage, gtrace.GetBaggageMap(ctx).String()),
	}

	reqEncoding := gconv.String(r.GetHeader("Content-Encoding"))
	if reqEncoding == "" {
		// Request content logging.
		reqBodyContentBytes, _ := ioutil.ReadAll(r.Body)
		r.Body = utils.NewReadCloser(reqBodyContentBytes, false)
		reqAttrs = append(reqAttrs, attribute.String(tracingEventHttpRequestBody, gstr.StrLimit(
			string(reqBodyContentBytes),
			gtrace.MaxContentLogSize(),
			"...",
		)))
	}

	span.AddEvent(tracingEventHttpRequest, trace.WithAttributes(
		reqAttrs...,
	))

	// Continue executing.
	r.Middleware.Next()

	// Error logging.
	if err := r.GetError(); err != nil {
		span.SetStatus(codes.Error, fmt.Sprintf(`%+v`, err))
	}

	respAttrs := []attribute.KeyValue{
		attribute.String(tracingEventHttpResponseHeaders, gconv.String(httputil.HeaderToMap(r.Response.Header()))),
	}
	// Response content logging.
	respEncoding := ""
	if r.Response.Header() != nil {
		respEncoding = gconv.String(r.Response.Header().Get("Content-Encoding"))
	}
	if respEncoding == "" {
		var resBodyContent = gstr.StrLimit(r.Response.BufferString(), gtrace.MaxContentLogSize(), "...")
		respAttrs = append(respAttrs, attribute.String(tracingEventHttpResponseBody, resBodyContent))
	}
	span.AddEvent(tracingEventHttpResponse, trace.WithAttributes(
		respAttrs...,
	))
}

func getSpanContext(ctx context.Context, header http.Header) context.Context {
	traceID := header.Get("MF-X-TRACE-ID")
	if traceID == "" {
		return otel.GetTextMapPropagator().Extract(
			ctx,
			propagation.HeaderCarrier(header),
		)
	}
	generatedTraceID, err := trace.TraceIDFromHex(traceID)
	if err != nil {
		return otel.GetTextMapPropagator().Extract(
			ctx,
			propagation.HeaderCarrier(header),
		)
	}
	return trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: generatedTraceID,
		SpanID:  tracing.NewSpanID(),
	}))
}
