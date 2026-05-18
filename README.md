# Spot Instance Lifecycle Exporter

A robust, fault-tolerant Prometheus exporter for AWS EC2 instance lifecycle events, including Spot interruptions, Rebalance recommendations, and Scheduled maintenance.

## Key Features (v2.0.0 Rewrite)

*   **Asynchronous Polling:** Decouples IMDS calls from Prometheus scrapes. A background poller ensures `/metrics` scrapes are instantaneous and never block on AWS API latency.
*   **IMDS Version Auto-Negotiation:** Automatically detects and uses the most secure version available (IMDSv2 with token management or IMDSv1).
*   **Comprehensive Lifecycle Events:**
    *   **Spot Interruption Notices:** The 2-minute warning.
    *   **Rebalance Recommendations:** Early signals of elevated disruption risk.
    *   **Scheduled Maintenance:** Hardware and OS maintenance notifications.
*   **Rich Contextual Labels:** All metrics include `instance_id`, `instance_type`, `availability_zone`, `region`, and `instance_life_cycle` (spot vs on-demand).
*   **Kubernetes Integration:** Optionally attach Kubernetes node labels to metrics for seamless correlation.

## Configuration

The exporter can be configured via CLI flags:

```text
Usage of ./spot-termination-exporter:
  -attach-node-labels
        attach labels from Kubernetes node (requires NODE_NAME env)
  -bind-addr string
        bind address for the metrics server (default ":9189")
  -kubeconfig string
        path to kubeconfig file
  -log-level string
        log level (debug, info, warn, error) (default "info")
  -metadata-endpoint string
        metadata endpoint to query (default "http://169.254.169.254/latest/meta-data/")
  -metrics-path string
        path to metrics endpoint (default "/metrics")
  -poll-interval duration
        interval to poll IMDS for events (default 5s)
  -token-endpoint string
        token endpoint to query for IMDSv2 (default "http://169.254.169.254/latest/api/token")
```

## Metrics

All metrics include the following common labels: `instance_id`, `instance_type`, `availability_zone`, `region`, `instance_life_cycle`.

| Metric Name | Type | Description |
| :--- | :--- | :--- |
| `aws_instance_termination_imminent` | Gauge | 1 if a spot termination is scheduled, 0 otherwise. Includes `instance_action` label. |
| `aws_instance_termination_in` | Gauge | Seconds until the instance is terminated. |
| `aws_instance_rebalance_recommended` | Gauge | 1 if AWS recommends rebalancing, 0 otherwise. |
| `aws_instance_scheduled_maintenance_active` | Gauge | 1 if a maintenance event is scheduled, 0 otherwise. |
| `aws_instance_metadata_service_available` | Gauge | 1 if IMDS is reachable, 0 otherwise. |
| `aws_instance_imds_version` | Gauge | The active IMDS version (1 or 2). |
| `spot_termination_exporter_last_poll_successful_timestamp_seconds` | Gauge | Unix timestamp of the last successful internal cache update. |

## Local Testing

### Using the Integration Test
The project includes an integration test that uses [amazon-ec2-metadata-mock](https://github.com/aws/amazon-ec2-metadata-mock) to simulate real AWS behaviors.

```bash
# Requires Docker daemon running
go test -v -run TestWithAEMM
```

### Manual Testing with AEMM
You can run the mock container manually and point the exporter to it:

```bash
docker run -d -p 1337:1337 public.ecr.aws/aws-ec2/amazon-ec2-metadata-mock:latest --spot-itn
./spot-termination-exporter --metadata-endpoint http://localhost:1337/latest/meta-data/ --log-level debug
```

## Building

```bash
go build -o spot-termination-exporter .
```
