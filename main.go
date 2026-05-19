package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/menionlabs/spot-termination-exporter/pkg/cache"
	"github.com/menionlabs/spot-termination-exporter/pkg/exporter"
	"github.com/menionlabs/spot-termination-exporter/pkg/imds"
	"github.com/menionlabs/spot-termination-exporter/pkg/k8s"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	bindAddr         = flag.String("bind-addr", ":9189", "bind address for the metrics server")
	metricsPath      = flag.String("metrics-path", "/metrics", "path to metrics endpoint")
	logLevel         = flag.String("log-level", "info", "log level (debug, info, warn, error)")
	metadataEndpoint = flag.String("metadata-endpoint", "http://169.254.169.254/latest/meta-data/", "metadata endpoint to query")
	tokenEndpoint    = flag.String("token-endpoint", "http://169.254.169.254/latest/api/token", "token endpoint to query")
	pollInterval     = flag.Duration("poll-interval", 5*time.Second, "interval to poll IMDS for events")
	attachNodeLabels = flag.Bool("attach-node-labels", false, "attach labels from node")
	kubeconfig       = flag.String("kubeconfig", "", "path to kubeconfig file")
)

func main() {
	flag.Parse()

	// 1. Setup Logging
	var level slog.Level
	switch *logLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("Starting spot-termination-exporter", "version", "v2.0.0")

	// 2. Initialize Cache
	store := cache.NewStore()

	// 3. Initialize IMDS Client and Poller
	imdsClient := imds.NewClient(*metadataEndpoint, *tokenEndpoint, 2*time.Second, logger)
	poller := imds.NewPoller(imdsClient, store, *pollInterval, logger)

	// 4. Initial IMDS Negotiation & Static Metadata Fetch
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := imdsClient.Negotiate(ctx); err != nil {
		logger.Error("failed to negotiate IMDS version, proceeding with unavailable state", "error", err)
	} else {
		if err := poller.FetchStaticMetadata(ctx); err != nil {
			logger.Error("failed to fetch initial static metadata", "error", err)
		}
	}

	// 5. Fetch Kubernetes Labels if requested
	var nodeLabels prometheus.Labels
	if *attachNodeLabels {
		labels, err := k8s.GetNodeLabels(*kubeconfig)
		if err != nil {
			logger.Error("failed to get node labels", "error", err)
			os.Exit(1)
		}
		nodeLabels = labels
		logger.Info("successfully fetched node labels", "count", len(labels))
	}

	// 6. Register Prometheus Collector
	collector := exporter.NewCollector(store, nodeLabels)
	prometheus.MustRegister(collector)

	// 7. Start Background Poller
	pollCtx, pollCancel := context.WithCancel(context.Background())
	defer pollCancel()
	go poller.Run(pollCtx)

	// 8. Start Metrics Server
	mux := http.NewServeMux()
	mux.Handle(*metricsPath, promhttp.Handler())
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
			<head><title>Spot Termination Exporter</title></head>
			<body>
			<h1>Spot Termination Exporter</h1>
			<p><a href="` + *metricsPath + `">Metrics</a></p>
			</body>
			</html>`))
	})

	server := &http.Server{
		Addr:    *bindAddr,
		Handler: mux,
	}

	go func() {
		logger.Info("starting metrics server", "addr", *bindAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", err)
			os.Exit(1)
		}
	}()

	// 9. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down exporter")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", "error", err)
	}

	logger.Info("exporter exited")
}
