package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"testing"
	"time"
)

func TestOrigins(t *testing.T) {
	for _, tc := range []struct {
		value string
		set   bool
		want  []string
	}{{"", false, []string{"http://localhost:5173"}}, {`["https://one.example","https://two.example"]`, true, []string{"https://one.example", "https://two.example"}}, {"https://one.example, https://two.example", true, []string{"https://one.example", "https://two.example"}}} {
		got, err := parseOrigins(tc.value, tc.set)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("parseOrigins(%q) = %v, %v", tc.value, got, err)
		}
	}
}

func TestServeGracefullyWaitsForActiveRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write([]byte("done")) })}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, server, listener) }()
	result := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String())
		if err == nil {
			defer resp.Body.Close()
			b, e := io.ReadAll(resp.Body)
			if e != nil || string(b) != "done" {
				err = fmt.Errorf("response %q: %v", b, e)
			}
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("server stopped before request completed: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request did not finish")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}
