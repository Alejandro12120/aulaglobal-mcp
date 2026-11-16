package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Exercise net/http's background read after consuming the complete request body.
func TestHTTPReadTimeoutAndDisconnect(t *testing.T) {
	for _, timeout := range []time.Duration{40 * time.Millisecond, 0} {
		t.Run(timeout.String(), func(t *testing.T) {
			consumed := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				io.Copy(io.Discard, r.Body)
				close(consumed)
				select {
				case <-r.Context().Done():
					close(cancelled)
				case <-release:
				}
			}))
			ts.Config.ReadTimeout = timeout
			ts.Start()
			defer ts.Close()
			defer close(release)
			conn, err := net.Dial("tcp", ts.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			io.Copy(conn, strings.NewReader("POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 1\r\n\r\nx"))
			<-consumed
			select {
			case <-cancelled:
				t.Fatal("request context cancelled while client remained connected")
			case <-time.After(150 * time.Millisecond):
			}
			conn.Close()
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("disconnect did not cancel request")
			}
		})
	}
}
