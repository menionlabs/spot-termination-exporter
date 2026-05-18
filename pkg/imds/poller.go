package imds

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"log/slog"

	"github.com/maskedweaver/spot-termination-exporter/pkg/cache"
)

type instanceAction struct {
	Action string    `json:"action"`
	Time   time.Time `json:"time"`
}

type instanceEvent struct {
	NoticeTime time.Time `json:"noticeTime"`
}

type Poller struct {
	client   *Client
	store    *cache.Store
	interval time.Duration
	logger   *slog.Logger
}

func NewPoller(client *Client, store *cache.Store, interval time.Duration, logger *slog.Logger) *Poller {
	return &Poller{
		client:   client,
		store:    store,
		interval: interval,
		logger:   logger,
	}
}

// FetchStaticMetadata populates the cache with immutable instance data.
func (p *Poller) FetchStaticMetadata(ctx context.Context) error {
	p.logger.Info("fetching static metadata")

	instanceID, status, err := p.client.Get(ctx, "meta-data/instance-id")
	if err != nil {
		return fmt.Errorf("failed to fetch instance-id: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("failed to fetch instance-id: status %d", status)
	}

	instanceType, status, err := p.client.Get(ctx, "meta-data/instance-type")
	if err != nil {
		return fmt.Errorf("failed to fetch instance-type: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("failed to fetch instance-type: status %d", status)
	}

	az, status, err := p.client.Get(ctx, "meta-data/placement/availability-zone")
	if err != nil {
		return fmt.Errorf("failed to fetch availability-zone: %w", err)
	}
	if status != 200 {
		return fmt.Errorf("failed to fetch availability-zone: status %d", status)
	}

	lifecycle, status, _ := p.client.Get(ctx, "meta-data/instance-life-cycle")
	lifecycleStr := "on-demand"
	if status == 200 {
		lifecycleStr = string(lifecycle)
	}

	region := string(az)
	if len(region) > 0 {
		region = region[:len(region)-1] // Remove AZ suffix
	}

	p.store.Update(func(s *cache.LifecycleState) {
		s.InstanceID = string(instanceID)
		s.InstanceType = string(instanceType)
		s.AvailabilityZone = string(az)
		s.Region = region
		s.Lifecycle = lifecycleStr
		s.IMDSVersion = p.client.Version()
		s.IMDSAvailable = true
	})

	return nil
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.poll(ctx)
		}
	}
}

func (p *Poller) poll(ctx context.Context) {
	p.logger.Debug("polling IMDS for events")

	success := true

	// 1. Spot Termination
	if err := p.pollSpotAction(ctx); err != nil {
		success = false
	}

	// 2. Rebalance Recommendation
	if err := p.pollRebalance(ctx); err != nil {
		success = false
	}

	// 3. Scheduled Maintenance
	if err := p.pollMaintenance(ctx); err != nil {
		success = false
	}

	p.store.Update(func(s *cache.LifecycleState) {
		if success {
			s.LastPollSuccessful = time.Now()
		}
		s.IMDSAvailable = success
	})
}

func (p *Poller) pollSpotAction(ctx context.Context) error {
	body, status, err := p.client.Get(ctx, "meta-data/spot/instance-action")
	if err != nil {
		p.logger.Error("failed to poll spot action", "error", err)
		return err
	}

	if status == 404 {
		p.store.Update(func(s *cache.LifecycleState) {
			s.TerminationImminent = false
			s.TerminationAction = ""
		})
		return nil
	}

	if status != 200 {
		p.logger.Error("unexpected status polling spot action", "status", status)
		return fmt.Errorf("unexpected status: %d", status)
	}

	var ia instanceAction
	if err := json.Unmarshal(body, &ia); err != nil {
		p.logger.Error("failed to unmarshal spot action", "error", err)
		return err
	}

	p.store.Update(func(s *cache.LifecycleState) {
		s.TerminationImminent = true
		s.TerminationAction = ia.Action
		s.TerminationTime = ia.Time
	})
	return nil
}

func (p *Poller) pollRebalance(ctx context.Context) error {
	body, status, err := p.client.Get(ctx, "meta-data/events/recommendations/rebalance")
	if err != nil {
		p.logger.Error("failed to poll rebalance recommendation", "error", err)
		return err
	}

	if status == 404 {
		p.store.Update(func(s *cache.LifecycleState) { s.RebalanceRecommended = false })
		return nil
	}

	if status != 200 {
		p.logger.Error("unexpected status polling rebalance recommendation", "status", status)
		return fmt.Errorf("unexpected status: %d", status)
	}

	var ie instanceEvent
	if err := json.Unmarshal(body, &ie); err != nil {
		p.logger.Error("failed to unmarshal rebalance event", "error", err)
		return err
	}

	p.store.Update(func(s *cache.LifecycleState) {
		s.RebalanceRecommended = true
		s.RebalanceTime = ie.NoticeTime
	})
	return nil
}

func (p *Poller) pollMaintenance(ctx context.Context) error {
	body, status, err := p.client.Get(ctx, "meta-data/events/maintenance/scheduled")
	if err != nil {
		p.logger.Error("failed to poll scheduled maintenance", "error", err)
		return err
	}

	if status == 404 {
		p.store.Update(func(s *cache.LifecycleState) { s.MaintenanceActive = false })
		return nil
	}

	if status != 200 {
		p.logger.Error("unexpected status polling scheduled maintenance", "status", status)
		return fmt.Errorf("unexpected status: %d", status)
	}

	// Maintenance returns a list of events if active
	active := len(strings.TrimSpace(string(body))) > 0 && string(body) != "[]"
	p.store.Update(func(s *cache.LifecycleState) {
		s.MaintenanceActive = active
	})
	return nil
}
