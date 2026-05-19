package imds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"log/slog"
	"os"

	"github.com/menionlabs/spot-termination-exporter/pkg/cache"
)

func TestIMDSVersionNegotiation(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))

	t.Run("IMDSv2_Success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" && r.URL.Path == "/latest/api/token" {
				w.Write([]byte("mock-token"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := NewClient(ts.URL+"/latest/", ts.URL+"/latest/api/token", time.Second, logger)
		err := client.Negotiate(context.Background())
		if err != nil {
			t.Fatalf("Negotiate failed: %v", err)
		}
		if client.Version() != 2 {
			t.Errorf("Expected version 2, got %d", client.Version())
		}
	})

	t.Run("IMDSv1_Fallback", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "PUT" && r.URL.Path == "/latest/api/token" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.URL.Path == "/latest/meta-data/instance-id" {
				w.Write([]byte("i-123"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		client := NewClient(ts.URL+"/latest/", ts.URL+"/latest/api/token", time.Second, logger)
		err := client.Negotiate(context.Background())
		if err != nil {
			t.Fatalf("Negotiate failed: %v", err)
		}
		if client.Version() != 1 {
			t.Errorf("Expected version 1, got %d", client.Version())
		}
	})
}

func TestPoller(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	store := cache.NewStore()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest/meta-data/instance-id":
			w.Write([]byte("i-123"))
		case "/latest/meta-data/instance-type":
			w.Write([]byte("t3.medium"))
		case "/latest/meta-data/placement/availability-zone":
			w.Write([]byte("us-east-1a"))
		case "/latest/meta-data/instance-life-cycle":
			w.Write([]byte("spot"))
		case "/latest/meta-data/spot/instance-action":
			w.Write([]byte(`{"action": "terminate", "time": "2026-05-18T12:00:00Z"}`))
		case "/latest/meta-data/events/recommendations/rebalance":
			w.WriteHeader(http.StatusNotFound)
		case "/latest/meta-data/events/maintenance/scheduled":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client := NewClient(ts.URL+"/latest/", "", time.Second, logger)
	client.version = 1 // Force v1 for simplicity

	poller := NewPoller(client, store, 100*time.Millisecond, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := poller.FetchStaticMetadata(ctx); err != nil {
		t.Fatalf("FetchStaticMetadata failed: %v", err)
	}

	state := store.Get()
	if state.InstanceID != "i-123" {
		t.Errorf("Expected instance ID i-123, got %s", state.InstanceID)
	}
	if state.Region != "us-east-1" {
		t.Errorf("Expected region us-east-1, got %s", state.Region)
	}

	// Run one poll manually
	poller.poll(ctx)

	state = store.Get()
	if !state.TerminationImminent {
		t.Error("Expected termination to be imminent")
	}
	if state.TerminationAction != "terminate" {
		t.Errorf("Expected action terminate, got %s", state.TerminationAction)
	}
}
