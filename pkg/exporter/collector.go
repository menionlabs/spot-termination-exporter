package exporter

import (
	"time"

	"github.com/menionlabs/spot-termination-exporter/pkg/cache"
	"github.com/prometheus/client_golang/prometheus"
)

type Collector struct {
	store                *cache.Store
	nodeLabels           prometheus.Labels
	imdsAvailable        *prometheus.Desc
	imdsEventsAvailable  *prometheus.Desc
	terminationImminent  *prometheus.Desc
	terminationIn        *prometheus.Desc
	rebalanceRecommended *prometheus.Desc
	maintenanceActive    *prometheus.Desc
	imdsVersion          *prometheus.Desc
	lastPollSuccess      *prometheus.Desc
}

func NewCollector(store *cache.Store, nodeLabels prometheus.Labels) *Collector {
	commonLabels := []string{
		"instance_id",
		"instance_type",
		"availability_zone",
		"region",
		"instance_life_cycle",
	}

	return &Collector{
		store:      store,
		nodeLabels: nodeLabels,
		imdsAvailable: prometheus.NewDesc(
			"aws_instance_metadata_service_available",
			"Metadata service available",
			commonLabels, nodeLabels,
		),
		imdsEventsAvailable: prometheus.NewDesc(
			"aws_instance_metadata_service_events_available",
			"Metadata service events endpoint available",
			commonLabels, nodeLabels,
		),
		terminationImminent: prometheus.NewDesc(
			"aws_instance_termination_imminent",
			"Instance is about to be terminated",
			append(commonLabels, "instance_action"), nodeLabels,
		),
		terminationIn: prometheus.NewDesc(
			"aws_instance_termination_in",
			"Instance will be terminated in seconds",
			commonLabels, nodeLabels,
		),
		rebalanceRecommended: prometheus.NewDesc(
			"aws_instance_rebalance_recommended",
			"Instance rebalance is recommended",
			commonLabels, nodeLabels,
		),
		maintenanceActive: prometheus.NewDesc(
			"aws_instance_scheduled_maintenance_active",
			"Instance has scheduled maintenance",
			commonLabels, nodeLabels,
		),
		imdsVersion: prometheus.NewDesc(
			"aws_instance_imds_version",
			"IMDS version in use (1 or 2)",
			commonLabels, nodeLabels,
		),
		lastPollSuccess: prometheus.NewDesc(
			"spot_termination_exporter_last_poll_successful_timestamp_seconds",
			"Unix timestamp of the last successful internal cache update",
			commonLabels, nodeLabels,
		),
	}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.imdsAvailable
	ch <- c.imdsEventsAvailable
	ch <- c.terminationImminent
	ch <- c.terminationIn
	ch <- c.rebalanceRecommended
	ch <- c.maintenanceActive
	ch <- c.imdsVersion
	ch <- c.lastPollSuccess
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	state := c.store.Get()

	labelValues := []string{
		state.InstanceID,
		state.InstanceType,
		state.AvailabilityZone,
		state.Region,
		state.Lifecycle,
	}

	// 1. Availability
	ch <- prometheus.MustNewConstMetric(c.imdsAvailable, prometheus.GaugeValue, boolToFloat(state.IMDSAvailable), labelValues...)
	ch <- prometheus.MustNewConstMetric(c.imdsEventsAvailable, prometheus.GaugeValue, boolToFloat(state.IMDSEventsAvailable), labelValues...)

	// 2. Termination
	termImminent := 0.0
	if state.TerminationImminent {
		termImminent = 1.0
	}
	ch <- prometheus.MustNewConstMetric(c.terminationImminent, prometheus.GaugeValue, termImminent, append(labelValues, state.TerminationAction)...)

	if state.TerminationImminent && !state.TerminationTime.IsZero() {
		delta := time.Until(state.TerminationTime).Seconds()
		if delta < 0 {
			delta = 0
		}
		ch <- prometheus.MustNewConstMetric(c.terminationIn, prometheus.GaugeValue, delta, labelValues...)
	}

	// 3. Rebalance
	ch <- prometheus.MustNewConstMetric(c.rebalanceRecommended, prometheus.GaugeValue, boolToFloat(state.RebalanceRecommended), labelValues...)

	// 4. Maintenance
	ch <- prometheus.MustNewConstMetric(c.maintenanceActive, prometheus.GaugeValue, boolToFloat(state.MaintenanceActive), labelValues...)

	// 5. Health
	ch <- prometheus.MustNewConstMetric(c.imdsVersion, prometheus.GaugeValue, float64(state.IMDSVersion), labelValues...)
	ch <- prometheus.MustNewConstMetric(c.lastPollSuccess, prometheus.GaugeValue, float64(state.LastPollSuccessful.Unix()), labelValues...)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
