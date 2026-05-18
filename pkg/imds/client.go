package imds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"log/slog"
)

const (
	defaultBaseURL      = "http://169.254.169.254/latest/"
	defaultTokenURL     = "http://169.254.169.254/latest/api/token"
	tokenTTLHeader      = "X-aws-ec2-metadata-token-ttl-seconds"
	tokenHeader         = "X-aws-ec2-metadata-token"
	defaultTokenTTL     = "21600" // 6 hours
)

type Client struct {
	httpClient *http.Client
	baseURL    string
	tokenURL   string
	logger     *slog.Logger

	mu          sync.RWMutex
	token       string
	tokenExpiry time.Time
	version     int // 1 or 2
}

func NewClient(baseURL, tokenURL string, timeout time.Duration, logger *slog.Logger) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	if tokenURL == "" {
		tokenURL = defaultTokenURL
	}

	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		baseURL:    baseURL,
		tokenURL:   tokenURL,
		logger:     logger,
	}
}

// Negotiate attempts to detect IMDS version and fetch a token if v2.
func (c *Client) Negotiate(ctx context.Context) error {
	c.logger.Debug("negotiating IMDS version")

	// Try IMDSv2 first
	token, err := c.refreshToken(ctx)
	if err == nil {
		c.mu.Lock()
		c.token = token
		c.tokenExpiry = time.Now().Add(time.Hour) // Rough estimate for now, will refine
		c.version = 2
		c.mu.Unlock()
		c.logger.Info("IMDSv2 detected and token acquired")
		return nil
	}

	c.logger.Debug("IMDSv2 failed, trying IMDSv1", "error", err)

	// Fallback to IMDSv1
	req, _ := http.NewRequestWithContext(ctx, "GET", c.baseURL+"meta-data/instance-id", nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach IMDSv1: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		c.mu.Lock()
		c.version = 1
		c.token = ""
		c.mu.Unlock()
		c.logger.Info("IMDSv1 detected")
		return nil
	}

	return fmt.Errorf("could not detect IMDS version, status: %d", resp.StatusCode)
}

func (c *Client) refreshToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "PUT", c.tokenURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(tokenTTLHeader, defaultTokenTTL)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch IMDSv2 token, status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (c *Client) Get(ctx context.Context, path string) ([]byte, int, error) {
	c.mu.RLock()
	version := c.version
	token := c.token
	c.mu.RUnlock()

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, 0, err
	}

	if version == 2 && token != "" {
		req.Header.Set(tokenHeader, token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && version == 2 {
		// Token might have expired, try to refresh once
		newToken, refreshErr := c.refreshToken(ctx)
		if refreshErr == nil {
			c.mu.Lock()
			c.token = newToken
			c.mu.Unlock()
			req.Header.Set(tokenHeader, newToken)
			resp, err = c.httpClient.Do(req)
			if err != nil {
				return nil, 0, err
			}
			defer resp.Body.Close()
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}

	return body, resp.StatusCode, nil
}

func (c *Client) Version() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}
