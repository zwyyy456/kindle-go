package server

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/flashdict/kindle2flashdict/internal/task"
)

func TestRunContextShutsDownListenersAndReleasesRuntimeLock(t *testing.T) {
	handler, _, taskService, storage := newHTTPTestHandler(t)
	webAddr := freeServerAddr(t)
	kindleAddr := freeServerAddr(t)
	runner := task.NewRunner(taskService, nil)
	server := NewServer(Config{WebAddr: webAddr, KindleAddr: kindleAddr}, handler, runner, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.RunContext(ctx) }()

	waitForServerListener(t, webAddr, done)
	waitForServerListener(t, kindleAddr, done)
	if _, ok, err := storage.RuntimeLock(context.Background()); err != nil || !ok {
		t.Fatalf("runtime lock while serving = %v, %v", ok, err)
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunContext error after cancellation = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunContext did not stop after cancellation")
	}

	for _, address := range []string{webAddr, kindleAddr} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatalf("listener %s was not released: %v", address, err)
		}
		_ = listener.Close()
	}
	if _, ok, err := storage.RuntimeLock(context.Background()); err != nil || ok {
		t.Fatalf("runtime lock after shutdown = %v, %v", ok, err)
	}
}

func freeServerAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func waitForServerListener(t *testing.T, address string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return
		}
		select {
		case runErr := <-done:
			t.Fatalf("server stopped before listener %s was ready: %v", address, runErr)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("listener %s did not start", address)
}
