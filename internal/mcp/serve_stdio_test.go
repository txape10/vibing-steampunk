package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func TestServeStdio_EOFAndCancelAreCleanExits(t *testing.T) {
	s := NewServer(&Config{BaseURL: "http://127.0.0.1:1", Username: "u", Password: "p", Client: "001", Language: "EN"})
	if err := s.serveStdio(context.Background(), strings.NewReader(""), io.Discard); err != nil {
		t.Fatalf("closed stdin must be a clean exit, got %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	defer pw.Close()
	done := make(chan error, 1)
	go func() { done <- s.serveStdio(ctx, pr, io.Discard) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a signal-driven shutdown must be a clean exit, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveStdio did not return after its context ended")
	}
}

func TestStdioShutdown(t *testing.T) {
	for _, err := range []error{nil, context.Canceled, io.EOF, io.ErrClosedPipe} {
		if !stdioShutdown(err) {
			t.Errorf("%v should be a shutdown", err)
		}
	}
	if stdioShutdown(io.ErrUnexpectedEOF) {
		t.Error("an unexpected EOF is a real error")
	}
}
