package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/raj/fluid/pkg/consensus"
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
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
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

	mux.HandleFunc("/v1/services/", func(w http.ResponseWriter, r *http.Request) {
		// GET /v1/services/{name}
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
		res, err := lookup(r.Context(), name)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("/raft/join", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		a, ok := cc.(*consensus.RaftAdapter)
		if !ok {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		var req joinReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.Addr == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
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

	mux.HandleFunc("/raft/snapshot", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		a, ok := cc.(*consensus.RaftAdapter)
		if !ok {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		if !a.IsLeader() {
			w.Header().Set("Location", fmt.Sprintf("http://%s", a.LeaderAddress()))
			w.WriteHeader(http.StatusTemporaryRedirect)
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

	mux.HandleFunc("/v1/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if createApp == nil {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		var req struct {
			AppID string `json:"appId"`
			IP    string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("invalid json"))
			return
		}
		if req.AppID == "" || req.IP == "" {
			w.WriteHeader(http.StatusBadRequest)
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

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("http server error", "error", err)
		}
	}()
	return func(shutdownCtx context.Context) error { return srv.Shutdown(shutdownCtx) }
}
