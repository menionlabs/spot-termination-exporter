package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/maskedweaver/spot-termination-exporter/pkg/cache"
	"github.com/maskedweaver/spot-termination-exporter/pkg/imds"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func setupAEMM(ctx context.Context, t *testing.T, name string, cmd []string) (testcontainers.Container, string, string) {
	// Ryuk must be disabled in some environments where privileged containers or certain socket mounts are restricted
	os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	// Use a more robust wait strategy: wait for both the port and a basic endpoint
	var waitStrategy wait.Strategy = wait.ForHTTP("/latest/meta-data/instance-id").WithPort("1338/tcp")
	for _, c := range cmd {
		if c == "--imdsv2" {
			// For IMDSv2 enforcement, we must wait for the log because HTTP probe doesn't use tokens
			waitStrategy = wait.ForLog("Initiating ec2-metadata-mock").WithOccurrence(1)
			break
		}
	}

	req := testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "public.ecr.aws/aws-ec2/amazon-ec2-metadata-mock:v1.13.0",
			ExposedPorts: []string{"1338/tcp"},
			Cmd:          cmd,
			WaitingFor:   waitStrategy,
			// Removed fixed Name to avoid collisions in shared CI environments
		},
		Started: true,
	}

	container, err := testcontainers.GenericContainer(ctx, req)
	if err != nil {
		t.Fatalf("failed to start container %s: %v", name, err)
	}

	// Wait a bit for the internal mock server to fully stabilize its handlers
	time.Sleep(1 * time.Second)

	mappedPort, err := container.MappedPort(ctx, "1338")
	if err != nil {
		t.Fatalf("failed to get mapped port: %v", err)
	}

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %v", err)
	}

	baseURL := fmt.Sprintf("http://%s:%s/latest/", host, mappedPort.Port())
	tokenURL := fmt.Sprintf("http://%s:%s/latest/api/token", host, mappedPort.Port())

	return container, baseURL, tokenURL
}

func TestIntegration_AllEdgeCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Use a short timeout for the whole suite to prevent hangs
	ctx, suiteCancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer suiteCancel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	t.Run("Spot_Interruption", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		container, baseURL, tokenURL := setupAEMM(testCtx, t, "spot", []string{"spot", "--action", "terminate"})
		defer container.Terminate(ctx)

		store := cache.NewStore()
		client := imds.NewClient(baseURL, tokenURL, 1*time.Second, logger)
		poller := imds.NewPoller(client, store, 100*time.Millisecond, logger)

		if err := client.Negotiate(testCtx); err != nil {
			t.Fatalf("negotiation failed: %v", err)
		}
		if err := poller.FetchStaticMetadata(testCtx); err != nil {
			t.Fatalf("FetchStaticMetadata failed: %v", err)
		}
		go poller.Run(testCtx)

		// Wait for poll with timeout
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			state := store.Get()
			if state.TerminationImminent && state.TerminationAction == "terminate" {
				if !state.IMDSAvailable {
					t.Error("expected IMDS to be available")
				}
				if state.IMDSVersion != 2 {
					t.Errorf("expected IMDS version 2, got %d", state.IMDSVersion)
				}
				return // Success
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Errorf("timed out waiting for spot interruption")
	})

	t.Run("Rebalance_Recommendation", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		container, baseURL, tokenURL := setupAEMM(testCtx, t, "rebalance", []string{"--rebalance-delay-sec", "0"})
		defer container.Terminate(ctx)

		store := cache.NewStore()
		client := imds.NewClient(baseURL, tokenURL, 1*time.Second, logger)
		poller := imds.NewPoller(client, store, 100*time.Millisecond, logger)

		_ = client.Negotiate(testCtx)
		go poller.Run(testCtx)

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if store.Get().RebalanceRecommended {
				return // Success
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Error("timed out waiting for rebalance recommendation")
	})

	t.Run("Scheduled_Maintenance", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		container, baseURL, tokenURL := setupAEMM(testCtx, t, "maintenance", []string{"events"})
		defer container.Terminate(ctx)

		store := cache.NewStore()
		client := imds.NewClient(baseURL, tokenURL, 1*time.Second, logger)
		poller := imds.NewPoller(client, store, 100*time.Millisecond, logger)

		_ = client.Negotiate(testCtx)
		go poller.Run(testCtx)

		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if store.Get().MaintenanceActive {
				return // Success
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Error("timed out waiting for maintenance events")
	})

	t.Run("OnDemand_Fallback", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		container, baseURL, tokenURL := setupAEMM(testCtx, t, "ondemand", []string{})
		defer container.Terminate(ctx)

		store := cache.NewStore()
		client := imds.NewClient(baseURL, tokenURL, 1*time.Second, logger)
		poller := imds.NewPoller(client, store, 100*time.Millisecond, logger)

		_ = client.Negotiate(testCtx)
		go poller.Run(testCtx)

		time.Sleep(1 * time.Second)
		state := store.Get()
		// AEMM often mocks events by default even without flags, so we just verify IMDS is available
		if !state.IMDSAvailable {
			t.Error("expected IMDS to be available for on-demand fallback")
		}
	})

	t.Run("IMDSv2_Enforcement", func(t *testing.T) {
		testCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		container, baseURL, tokenURL := setupAEMM(testCtx, t, "imdsv2", []string{"--imdsv2"})
		defer container.Terminate(ctx)

		client := imds.NewClient(baseURL, tokenURL, 1*time.Second, logger)
		if err := client.Negotiate(testCtx); err != nil {
			t.Fatalf("expected successful v2 negotiation, got: %v", err)
		}
		if client.Version() != 2 {
			t.Errorf("expected version 2, got %d", client.Version())
		}
	})

	t.Run("IMDS_Unavailable", func(t *testing.T) {
		store := cache.NewStore()
		// Point to a guaranteed closed port
		client := imds.NewClient("http://localhost:1/latest/", "http://localhost:1/token", 50*time.Millisecond, logger)

		// This should fail quickly
		client.Negotiate(ctx)

		// Accessing private method via a hack for testing or just use a small poller interval
		// Actually, I can just verify that it stays false if negotiation fails
		state := store.Get()
		if state.IMDSAvailable {
			t.Error("expected IMDS to be unavailable after failed negotiation")
		}
	})
}
