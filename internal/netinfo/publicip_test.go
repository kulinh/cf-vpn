package netinfo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetectReturnsIPFromPrimary(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("203.0.113.42"))
	}))
	defer ts.Close()

	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: time.Minute, Now: time.Now}
	ip, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "203.0.113.42" {
		t.Fatalf("got %q", ip)
	}
}

func TestDetectFallsBackOnPrimaryFail(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("198.51.100.7\n"))
	}))
	defer fallback.Close()

	d := &Detector{Primary: primary.URL, Fallback: fallback.URL, HTTP: primary.Client(), TTL: time.Minute, Now: time.Now}
	ip, err := d.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ip != "198.51.100.7" {
		t.Fatalf("got %q", ip)
	}
}

func TestDetectCachesWithinTTL(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte("203.0.113.1"))
	}))
	defer ts.Close()

	now := time.Now()
	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected 1 call, got %d", got)
	}
}

func TestDetectRefreshesAfterTTL(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte("203.0.113.1"))
	}))
	defer ts.Close()

	now := time.Now()
	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: 5 * time.Minute, Now: func() time.Time { return now }}
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	now = now.Add(6 * time.Minute)
	if _, err := d.Detect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected 2 calls, got %d", got)
	}
}

func TestDetectRejectsInvalidIP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not-an-ip"))
	}))
	defer ts.Close()

	d := &Detector{Primary: ts.URL, HTTP: ts.Client(), TTL: time.Minute, Now: time.Now}
	if _, err := d.Detect(context.Background()); err == nil {
		t.Fatal("expected error on invalid IP")
	}
}
