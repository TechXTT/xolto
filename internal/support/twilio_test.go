package support

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestTwilioClient(t *testing.T, handler http.Handler) (*TwilioClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// Swap out the Messages URL by creating a client whose httpClient will hit
	// the test server instead of api.twilio.com. We achieve this by patching the
	// URL at call time — see twilioClientWithBaseURL helper used in sms.go tests.
	// Here we verify TwilioClient behaviour directly via a round-trip through the
	// test server: we override the URL via the unexported helper below.
	hc := &http.Client{
		Transport: &rewriteTransport{base: srv.URL, inner: http.DefaultTransport},
	}
	c := NewTwilioClient("ACtest", "authtoken", hc)
	return c, srv
}

// rewriteTransport replaces the host+scheme of every outgoing request with
// the test server base URL. This lets us use the real TwilioClient.SendSMS
// path while directing traffic to httptest.
type rewriteTransport struct {
	base  string // e.g. "http://127.0.0.1:PORT"
	inner http.RoundTripper
}

func (rt *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = strings.TrimPrefix(rt.base, "http://")
	return rt.inner.RoundTrip(clone)
}

// ---------------------------------------------------------------------------
// TestTwilioClient_SendSMS_201
// ---------------------------------------------------------------------------

func TestTwilioClient_SendSMS_201(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected Content-Type: %s", r.Header.Get("Content-Type"))
		}
		_ = r.ParseForm()
		if r.Form.Get("To") == "" {
			t.Error("To field missing")
		}
		if r.Form.Get("From") == "" {
			t.Error("From field missing")
		}
		if r.Form.Get("Body") == "" {
			t.Error("Body field missing")
		}
		w.WriteHeader(http.StatusCreated)
	})

	c, _ := newTestTwilioClient(t, handler)
	err := c.SendSMS(context.Background(), "+15550001111", "+15550002222", "hello")
	if err != nil {
		t.Fatalf("expected nil error on 201, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestTwilioClient_SendSMS_5xx → ErrTwilioTransient
// ---------------------------------------------------------------------------

func TestTwilioClient_SendSMS_5xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	c, _ := newTestTwilioClient(t, handler)
	err := c.SendSMS(context.Background(), "+15550001111", "+15550002222", "hello")
	if err == nil {
		t.Fatal("expected error on 500, got nil")
	}
	if !errors.Is(err, ErrTwilioTransient) {
		t.Fatalf("expected ErrTwilioTransient, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestTwilioClient_SendSMS_4xx → ErrTwilioPermanent
// ---------------------------------------------------------------------------

func TestTwilioClient_SendSMS_4xx(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})

	c, _ := newTestTwilioClient(t, handler)
	err := c.SendSMS(context.Background(), "+15550001111", "+15550002222", "hello")
	if err == nil {
		t.Fatal("expected error on 401, got nil")
	}
	if !errors.Is(err, ErrTwilioPermanent) {
		t.Fatalf("expected ErrTwilioPermanent, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// TestTwilioClient_SendSMS_NetworkError → ErrTwilioTransient
// ---------------------------------------------------------------------------

func TestTwilioClient_SendSMS_NetworkError(t *testing.T) {
	// Point the client at a server that is already closed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	hc := &http.Client{
		Transport: &rewriteTransport{base: srv.URL, inner: http.DefaultTransport},
	}
	c := NewTwilioClient("ACtest", "authtoken", hc)
	err := c.SendSMS(context.Background(), "+15550001111", "+15550002222", "hello")
	if err == nil {
		t.Fatal("expected error on closed server, got nil")
	}
	if !errors.Is(err, ErrTwilioTransient) {
		t.Fatalf("expected ErrTwilioTransient on network error, got %v", err)
	}
}
