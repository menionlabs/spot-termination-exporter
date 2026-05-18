package exporter

import (
	"strings"
	"testing"
	"time"

	"github.com/maskedweaver/spot-termination-exporter/pkg/cache"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

func TestCollector(t *testing.T) {
	store := cache.NewStore()
	now := time.Now()

	store.Update(func(s *cache.LifecycleState) {
		s.InstanceID = "i-123"
		s.InstanceType = "t3.medium"
		s.AvailabilityZone = "us-east-1a"
		s.Region = "us-east-1"
		s.Lifecycle = "spot"
		s.TerminationImminent = true
		s.TerminationAction = "terminate"
		s.TerminationTime = now.Add(1 * time.Minute)
		s.IMDSAvailable = true
		s.IMDSVersion = 2
		s.LastPollSuccessful = now
	})

	nodeLabels := prometheus.Labels{"env": "prod"}
	collector := NewCollector(store, nodeLabels)

	ch := make(chan prometheus.Metric)
	go func() {
		collector.Collect(ch)
		close(ch)
	}()

	metrics := make(map[string]float64)
	for m := range ch {
		var metric dto.Metric
		m.Write(&metric)
		desc := m.Desc().String()
		if metric.Gauge != nil {
			if strings.Contains(desc, "aws_instance_termination_imminent") {
				metrics["termination"] = metric.Gauge.GetValue()
				// Verify labels
				foundEnv := false
				for _, lp := range metric.Label {
					if *lp.Name == "env" && *lp.Value == "prod" {
						foundEnv = true
					}
				}
				if !foundEnv {
					t.Error("Did not find node label 'env=prod' on metric")
				}
			}
			if strings.Contains(desc, "aws_instance_imds_version") {
				metrics["version"] = metric.Gauge.GetValue()
			}
		}
	}

	if metrics["termination"] != 1 {
		t.Errorf("Expected termination metric 1, got %v", metrics["termination"])
	}
	if metrics["version"] != 2 {
		t.Errorf("Expected version metric 2, got %v", metrics["version"])
	}
}
