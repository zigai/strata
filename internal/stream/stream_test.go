package stream

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadBounded(t *testing.T) {
	t.Parallel()

	t.Run("within limit", func(t *testing.T) {
		t.Parallel()

		data := []byte("hello world config")
		r := bytes.NewReader(data)

		got, err := ReadBounded(r, 1024)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !bytes.Equal(got, data) {
			t.Fatalf("got %q, want %q", string(got), string(data))
		}
	})

	t.Run("exceeds limit", func(t *testing.T) {
		t.Parallel()

		data := []byte(strings.Repeat("a", 200))
		r := bytes.NewReader(data)

		_, err := ReadBounded(r, 100)
		if err == nil {
			t.Fatalf("expected error, got nil")
		}

		if !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("expected ErrFileTooLarge, got %v", err)
		}
	})

	t.Run("zero maxBytes defaults to DefaultMaxFileSize", func(t *testing.T) {
		t.Parallel()

		data := []byte("small config")
		r := bytes.NewReader(data)

		got, err := ReadBounded(r, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !bytes.Equal(got, data) {
			t.Fatalf("got %q, want %q", string(got), string(data))
		}
	})
}

func TestReadStdin(t *testing.T) {
	// This test must not run in parallel: the stdin cache is package-global.
	resetForTesting()
	defer resetForTesting()

	initial := bytes.NewBufferString("port = 8080\n")

	first, err := ReadStdin(initial, 1024)
	if err != nil {
		t.Fatalf("first read error: %v", err)
	}

	if string(first) != "port = 8080\n" {
		t.Fatalf("first read got %q", string(first))
	}

	secondBuf := bytes.NewBufferString("port = 9090\n")

	second, err := ReadStdin(secondBuf, 1024)
	if err != nil {
		t.Fatalf("second read error: %v", err)
	}

	if string(second) != "port = 9090\n" {
		t.Fatalf("second read got %q, want port = 9090", string(second))
	}
}

func TestReadStdinPartialReadCorruptionPrevention(t *testing.T) {
	resetForTesting()
	defer resetForTesting()

	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	defer func() { _ = rPipe.Close() }()

	origStdin := os.Stdin
	os.Stdin = rPipe

	defer func() { os.Stdin = origStdin }()

	fullInput := "0123456789ABCDEFGHIJ"
	go func() {
		_, _ = wPipe.WriteString(fullInput)
		_ = wPipe.Close()
	}()

	_, readErr := ReadStdin(nil, 10)
	if !errors.Is(readErr, ErrFileTooLarge) {
		t.Fatalf("call 1: expected ErrFileTooLarge, got %v", readErr)
	}

	_, cachedErr := ReadStdin(nil, 100)
	if !errors.Is(cachedErr, ErrFileTooLarge) {
		t.Fatalf("call 2: expected ErrFileTooLarge (not corrupted partial read), got %v", cachedErr)
	}
}
