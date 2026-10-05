package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeleteShare(t *testing.T) {
	var got struct{ method, path, auth string }
	status, body := http.StatusNoContent, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.auth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	if err := deleteShare(context.Background(), srv.Client(), srv.URL, "01HXYZ", "tok"); err != nil {
		t.Fatalf("204: %v", err)
	}
	if got.method != http.MethodDelete || got.path != "/api/pins/01HXYZ" || got.auth != "Bearer tok" {
		t.Fatalf("sent %+v", got)
	}

	status = http.StatusNotFound
	if err := deleteShare(context.Background(), srv.Client(), srv.URL, "01HXYZ", "tok"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("404: %v", err)
	}
	status, body = http.StatusForbidden, "only the person who shared it can delete it"
	if err := deleteShare(context.Background(), srv.Client(), srv.URL, "01HXYZ", "tok"); err == nil || !strings.Contains(err.Error(), "http 403: only the person") {
		t.Fatalf("403: %v", err)
	}
}
