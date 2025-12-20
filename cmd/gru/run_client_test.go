package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBuildHTTPClientInsecureTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := buildHTTPClient(true, "", false, "", false, false)
	if err != nil {
		t.Fatalf("client: %v", err)
	}

	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()
}

func TestBuildHTTPClientProxyBypass(t *testing.T) {
	_ = os.Setenv("HTTP_PROXY", "http://127.0.0.1:9")
	_ = os.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	t.Cleanup(func() {
		os.Unsetenv("HTTP_PROXY")
		os.Unsetenv("HTTPS_PROXY")
	})

	client, err := buildHTTPClient(false, "", false, "", false, false)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if client.Transport.(*http.Transport).Proxy == nil {
		t.Fatalf("expected proxy function when noproxy=false")
	}

	client, err = buildHTTPClient(false, "", false, "", true, false)
	if err != nil {
		t.Fatalf("client noproxy: %v", err)
	}
	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatalf("expected proxy disabled when noproxy=true")
	}
}

func TestBuildHTTPClientDisableCookies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("Cookie"); c != "" {
			w.Write([]byte(c))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "sid", Value: "abc"})
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client, err := buildHTTPClient(false, "", false, "", false, false)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	// first request sets cookie
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get1: %v", err)
	}
	resp.Body.Close()
	// second request should send cookie
	resp, err = client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get2: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 64)
	n, _ := resp.Body.Read(buf)
	if n == 0 {
		t.Fatalf("expected cookie echoed back")
	}

	// disable cookies
	client, err = buildHTTPClient(false, "", false, "", false, true)
	if err != nil {
		t.Fatalf("client disable: %v", err)
	}
	resp, err = client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get3: %v", err)
	}
	resp.Body.Close()
	resp, err = client.Get(srv.URL)
	if err != nil {
		t.Fatalf("get4: %v", err)
	}
	defer resp.Body.Close()
	n, _ = resp.Body.Read(buf)
	if n != 0 {
		t.Fatalf("expected no cookie when disabled")
	}
}
