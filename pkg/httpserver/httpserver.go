package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/raj/fluid/pkg/consensus"
	"github.com/raj/fluid/pkg/metrics"
	"github.com/raj/fluid/pkg/types"
)

type CreateAppFunc func(ctx context.Context, appID, ip string) error
type LookupFunc func(ctx context.Context, name string) ([]types.ServiceEndpoint, error)

type joinReq struct {
	ID   string `json:"id"`
	Addr string `json:"addr"`
}

// Start registers health and readiness endpoints and launches an HTTP server.
// addr like "127.0.0.1:18080". It returns a shutdown function to stop the server (best-effort).
func Start(ctx context.Context, logger *slog.Logger, cc consensus.ConsensusClient, addr string, createApp CreateAppFunc, lookup LookupFunc) func(context.Context) error {
	mux := http.NewServeMux()

	// Health check endpoint with metrics
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/healthz", wrapHandlerFuncWithMetrics("/healthz", healthHandler))

	// Readiness check endpoint with metrics
	readyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if adapter, ok := cc.(*consensus.RaftAdapter); ok {
			leader := adapter.LeaderAddress()
			if leader == "" {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write([]byte("leader: unknown"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("leader: " + leader))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	mux.Handle("/readyz", wrapHandlerFuncWithMetrics("/readyz", readyHandler))

	// Add Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry(), promhttp.HandlerOpts{}))

	// Service lookup endpoint
	serviceHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/v1/services/")
		if name == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if lookup == nil {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}

		lookupStart := time.Now()
		res, err := lookup(r.Context(), name)
		lookupDuration := time.Since(lookupStart).Seconds()
		metrics.ObserveHTTPRequestDuration("LOOKUP", "/v1/services/{name}", lookupDuration)

		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}

		w.WriteHeader(http.StatusOK)
		responseBytes, _ := json.Marshal(res)
		metrics.ObserveHTTPRequestDuration("RESPONSE", "/v1/services/{name}", float64(len(responseBytes)))
		_, _ = w.Write(responseBytes)
	})
	mux.Handle("/v1/services/", wrapHandlerFuncWithMetrics("/v1/services/{name}", serviceHandler))

	// Raft join endpoint
	joinHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		a, ok := cc.(*consensus.RaftAdapter)
		if !ok {
			w.WriteHeader(http.StatusNotImplemented)
			metrics.RecordHTTPRequest("POST", "/raft/join", "501")
			return
		}

		decodeStart := time.Now()
		var req joinReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Addr == "" {
			w.WriteHeader(http.StatusBadRequest)
			metrics.RecordHTTPRequest("POST", "/raft/join", "400")
			metrics.ObserveHTTPRequestDuration("DECODE", "/raft/join", time.Since(decodeStart).Seconds())
			return
		}
		metrics.ObserveHTTPRequestDuration("DECODE", "/raft/join", time.Since(decodeStart).Seconds())

		jctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := a.AddVoter(jctx, req.ID, req.Addr); err != nil {
			if ne, notLeader := consensus.IsNotLeader(err); notLeader {
				w.Header().Set("Location", fmt.Sprintf("http://%s", ne.LeaderAddr))
				w.WriteHeader(http.StatusTemporaryRedirect)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.Handle("/raft/join", wrapHandlerFuncWithMetrics("/raft/join", joinHandler))

	// Raft snapshot endpoint
	snapshotHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			metrics.RecordHTTPRequest("POST", "/raft/snapshot", "405")
			return
		}

		a, ok := cc.(*consensus.RaftAdapter)
		if !ok {
			w.WriteHeader(http.StatusNotImplemented)
			metrics.RecordHTTPRequest("POST", "/raft/snapshot", "501")
			return
		}

		if !a.IsLeader() {
			leader := a.LeaderAddress()
			w.Header().Set("Location", fmt.Sprintf("http://%s", leader))
			w.WriteHeader(http.StatusTemporaryRedirect)
			metrics.RecordHTTPRequest("POST", "/raft/snapshot", "307")
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "not leader"})
			return
		}
		// Raft doesn't expose explicit trigger; one way is to call Snapshot() on raft
		if err := a.Raft().Snapshot().Error(); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.Handle("/raft/snapshot", wrapHandlerFuncWithMetrics("/raft/snapshot", snapshotHandler))

	// App creation endpoint with metrics
	appHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			metrics.RecordHTTPRequest("POST", "/v1/apps", "405")
			return
		}
		if createApp == nil {
			w.WriteHeader(http.StatusNotImplemented)
			metrics.RecordHTTPRequest("POST", "/v1/apps", "501")
			return
		}

		decodeStart := time.Now()
		var req struct {
			AppID string `json:"appId"`
			IP    string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			metrics.RecordHTTPRequest("POST", "/v1/apps", "400")
			metrics.ObserveHTTPRequestDuration("DECODE", "/v1/apps", time.Since(decodeStart).Seconds())
			_, _ = w.Write([]byte("invalid json"))
			return
		}
		metrics.ObserveHTTPRequestDuration("DECODE", "/v1/apps", time.Since(decodeStart).Seconds())

		if req.AppID == "" || req.IP == "" {
			w.WriteHeader(http.StatusBadRequest)
			metrics.RecordHTTPRequest("POST", "/v1/apps", "400")
			_, _ = w.Write([]byte("appId and ip required"))
			return
		}
		cctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := createApp(cctx, req.AppID, req.IP); err != nil {
			if ne, notLeader := consensus.IsNotLeader(err); notLeader {
				w.Header().Set("Location", fmt.Sprintf("http://%s", ne.LeaderAddr))
				w.WriteHeader(http.StatusTemporaryRedirect)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	})
	mux.Handle("/v1/apps", wrapHandlerFuncWithMetrics("/v1/apps", appHandler))

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
		}
	}()
	return func(shutdownCtx context.Context) error { return srv.Shutdown(shutdownCtx) }
}
