package k8s

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"
	"unicode"

	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func buildConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if cfg, err := rest.InClusterConfig(); err == nil {
		return cfg, nil
	}
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: ""},
		&clientcmd.ConfigOverrides{}).ClientConfig()
}

func sanitizeLabelName(name string) string {
	var invalidLabelCharRE = regexp.MustCompile(`[^a-zA-Z0-9_]`)
	sanitized := invalidLabelCharRE.ReplaceAllString(name, "_")
	if len(sanitized) > 0 && unicode.IsDigit(rune(sanitized[0])) {
		sanitized = "_" + sanitized
	}
	return sanitized
}

func GetNodeLabels(kubeconfig string) (prometheus.Labels, error) {
	nodeName := os.Getenv("NODE_NAME")
	if nodeName == "" {
		return nil, fmt.Errorf("required NODE_NAME environment variable not set")
	}

	cfg, err := buildConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	node, err := cs.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get node %q: %w", nodeName, err)
	}

	sanitizedLabels := make(prometheus.Labels)
	for k, v := range node.Labels {
		sanitizedLabels[sanitizeLabelName(k)] = v
	}

	return sanitizedLabels, nil
}
