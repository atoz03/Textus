package plugins

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAPluginsRequestsGoOverOneKeptConnection(t *testing.T) {
	var opened atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			opened.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{}}
	granted := []string{"network:*"}
	for range 3 {
		answer, err := Send(context.Background(), client, "test.kept", granted, HTTPRequest{URL: server.URL + "/"})
		if err != nil {
			t.Fatal(err)
		}
		if answer.Status != 200 || answer.Body != "b2s=" {
			t.Errorf("%+v", answer)
		}
	}
	if n := opened.Load(); n != 1 {
		t.Errorf("%d connections", n)
	}
}

func TestARequestToAHostNotGrantedIsRefused(t *testing.T) {
	_, err := Send(context.Background(), http.DefaultClient, "a.b", []string{"network:api.example.com"}, HTTPRequest{URL: "https://example.org/"})
	if err == nil || err.Error() != "a.b was not granted network:example.org." {
		t.Errorf("%v", err)
	}
}

func TestACancelledRequestAnswersAtOnce(t *testing.T) {
	id := "r1"
	go func() {
		time.Sleep(20 * time.Millisecond)
		Cancel("test.cancel", id)
	}()
	started := time.Now()
	_, err := Cancellable(context.Background(), "test.cancel", &id, func(ctx context.Context) (struct{}, error) {
		select {
		case <-time.After(60 * time.Second):
		case <-ctx.Done():
		}
		return struct{}{}, nil
	})
	if err == nil || err.Error() != Cancelled {
		t.Errorf("%v", err)
	}
	if time.Since(started) > 5*time.Second {
		t.Error("the cancel waited")
	}
	if isPending("test.cancel", id) {
		t.Error("the request is still kept")
	}
}

func TestASecondRequestUnderTheSameIDLeavesTheFirstRunning(t *testing.T) {
	id := "1"
	slow := func(n int) func(context.Context) (int, error) {
		return func(ctx context.Context) (int, error) {
			select {
			case <-time.After(80 * time.Millisecond):
				return n, nil
			case <-ctx.Done():
				return 0, errors.New("stopped")
			}
		}
	}
	first := make(chan int, 1)
	go func() {
		n, _ := Cancellable(context.Background(), "test.twice", &id, slow(1))
		first <- n
	}()
	time.Sleep(10 * time.Millisecond)
	second, err := Cancellable(context.Background(), "test.twice", &id, slow(2))
	if err != nil || second != 2 || <-first != 1 {
		t.Errorf("second %d %v", second, err)
	}
}
