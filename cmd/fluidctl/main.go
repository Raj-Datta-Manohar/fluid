package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/raj/fluid/pkg/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	server := flag.String("server", "http://127.0.0.1:18080", "agent base URL")
	mode := flag.String("mode", "create-app", "mode: create-app, raft-join, or lookup")
	appID := flag.String("app", "", "application ID")
	ip := flag.String("ip", "", "application IP")
	serviceName := flag.String("service", "", "service name (for lookup)")
	raftID := flag.String("raft-id", "", "raft server ID (for raft-join)")
	raftAddr := flag.String("raft-addr", "", "raft address host:port (for raft-join)")
	flag.Parse()

	// Create context with tracing
	ctx := context.Background()
	tracer := otel.Tracer(tracing.TracerCLI)

	switch *mode {
	case "create-app":
		if *appID == "" || *ip == "" {
			fmt.Fprintln(os.Stderr, "usage: fluidctl -mode create-app -server URL -app <id> -ip <ip>")
			os.Exit(2)
		}
		createApp(ctx, tracer, *server, *appID, *ip)
	case "raft-join":
		if *raftID == "" || *raftAddr == "" {
			fmt.Fprintln(os.Stderr, "usage: fluidctl -mode raft-join -server URL -raft-id <id> -raft-addr <host:port>")
			os.Exit(2)
		}
		raftJoin(ctx, tracer, *server, *raftID, *raftAddr)
	case "lookup":
		if *serviceName == "" {
			fmt.Fprintln(os.Stderr, "usage: fluidctl -mode lookup -server URL -service <name>")
			os.Exit(2)
		}
		lookupService(ctx, tracer, *server, *serviceName)
	default:
		fmt.Fprintln(os.Stderr, "unknown mode; use create-app, raft-join, or lookup")
		os.Exit(2)
	}
}

func createApp(ctx context.Context, tracer trace.Tracer, server, appID, ip string) {
	spanCtx, span := tracer.Start(ctx, tracing.SpanCLICreateApp, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	span.SetAttributes(
		attribute.String("app.id", appID),
		attribute.String("app.ip", ip),
		attribute.String("server.url", server),
	)

	payload, _ := json.Marshal(map[string]string{"appId": appID, "ip": ip})
	req, _ := http.NewRequestWithContext(spanCtx, "POST", server+"/v1/apps", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "request failed")
		fmt.Fprintln(os.Stderr, "request failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "HTTP error")
	}

	fmt.Println("status:", resp.Status)
	io.Copy(os.Stdout, resp.Body)
}

func raftJoin(ctx context.Context, tracer trace.Tracer, server, raftID, raftAddr string) {
	spanCtx, span := tracer.Start(ctx, tracing.SpanCLIRaftJoin, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	span.SetAttributes(
		attribute.String("raft.id", raftID),
		attribute.String("raft.addr", raftAddr),
		attribute.String("server.url", server),
	)

	payload, _ := json.Marshal(map[string]string{"id": raftID, "addr": raftAddr})
	req, _ := http.NewRequestWithContext(spanCtx, "POST", server+"/raft/join", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "request failed")
		fmt.Fprintln(os.Stderr, "request failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "HTTP error")
	}

	fmt.Println("status:", resp.Status)
	io.Copy(os.Stdout, resp.Body)
}

func lookupService(ctx context.Context, tracer trace.Tracer, server, serviceName string) {
	spanCtx, span := tracer.Start(ctx, tracing.SpanCLILookup, trace.WithSpanKind(trace.SpanKindClient))
	defer span.End()

	span.SetAttributes(
		attribute.String("service.name", serviceName),
		attribute.String("server.url", server),
	)

	req, _ := http.NewRequestWithContext(spanCtx, "GET", server+"/v1/services/"+serviceName, nil)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "request failed")
		fmt.Fprintln(os.Stderr, "request failed:", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))
	if resp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "HTTP error")
	}

	fmt.Println("status:", resp.Status)
	io.Copy(os.Stdout, resp.Body)
}
