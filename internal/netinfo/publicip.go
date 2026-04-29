package netinfo

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultPrimary  = "https://api.ipify.org"
	defaultFallback = "https://icanhazip.com"
	defaultTTL      = 5 * time.Minute
)

type Detector struct {
	Primary  string
	Fallback string
	HTTP     *http.Client
	TTL      time.Duration
	Now      func() time.Time

	mu       sync.Mutex
	cachedIP string
	cachedAt time.Time
}

func NewDefault() *Detector {
	return &Detector{
		Primary:  defaultPrimary,
		Fallback: defaultFallback,
		HTTP:     &http.Client{Timeout: 5 * time.Second},
		TTL:      defaultTTL,
		Now:      time.Now,
	}
}

func (d *Detector) Detect(ctx context.Context) (string, error) {
	d.mu.Lock()
	if d.cachedIP != "" && d.Now().Sub(d.cachedAt) < d.TTL {
		ip := d.cachedIP
		d.mu.Unlock()
		return ip, nil
	}
	d.mu.Unlock()

	ip, err := d.fetch(ctx, d.Primary)
	if err != nil && d.Fallback != "" {
		ip, err = d.fetch(ctx, d.Fallback)
	}
	if err != nil {
		return "", err
	}

	d.mu.Lock()
	d.cachedIP = ip
	d.cachedAt = d.Now()
	d.mu.Unlock()
	return ip, nil
}

func (d *Detector) fetch(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("ip lookup %s: status %d", url, resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", err
	}
	ip := strings.TrimSpace(string(raw))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid IP from %s: %q", url, ip)
	}
	return ip, nil
}
