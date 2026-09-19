package stream

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
)

const (
	// DefaultMaxFileSize is the size limit applied when a caller passes a
	// non-positive maxBytes: 1 MiB.
	DefaultMaxFileSize int64 = 1024 * 1024
)

var (
	// ErrFileTooLarge is returned when configuration input exceeds the maximum
	// permitted size. The message states the limit that was exceeded.
	ErrFileTooLarge = errors.New("configuration input exceeds maximum allowed size")

	stdinMu     sync.Mutex
	stdinBuffer []byte
	stdinCached bool
	errStdin    error
)

// ReadBounded reads from r up to maxBytes and returns the data read.
//
// A maxBytes of zero or less selects [DefaultMaxFileSize]. Note: one byte past
// the limit is read, and an input larger than the limit is reported, not
// returned truncated. A maxBytes of [math.MaxInt64] is used unchanged; that
// limit cannot be exceeded.
//
// It returns [ErrFileTooLarge] if the input exceeds the limit. The returned
// slice is owned by the caller.
func ReadBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileSize
	}

	limit := maxBytes
	if maxBytes < math.MaxInt64 {
		limit++
	}

	data, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, fmt.Errorf("read configuration data: %w", err)
	}

	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w (%d bytes limit, got >%d bytes)", ErrFileTooLarge, maxBytes, maxBytes)
	}

	return data, nil
}

// ReadStdin reads configuration from standard input, or from the reader the
// caller supplies in r.
//
// A nil r selects the process stdin. That reader, and only that reader, is
// cached for the lifetime of the process; a second read returns the data read
// the first time. A reader supplied by the caller is read directly on every
// call and is not cached. A successful call reads such a reader to end of input,
// and a second read over the same reader returns no data.
//
// A maxBytes of zero or less selects [DefaultMaxFileSize].
//
// It returns [ErrFileTooLarge] if the input, or the cached data, exceeds the
// limit. The returned slice is a copy owned by the caller. Reads of the process
// stdin are serialized by a mutex.
func ReadStdin(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileSize
	}

	reader := r
	if reader == nil {
		reader = os.Stdin
	}

	if reader != os.Stdin {
		return ReadBounded(reader, maxBytes)
	}

	stdinMu.Lock()
	defer stdinMu.Unlock()

	if stdinCached {
		if errStdin != nil {
			return nil, fmt.Errorf("read stdin configuration: %w", errStdin)
		}

		if int64(len(stdinBuffer)) > maxBytes {
			return nil, fmt.Errorf("%w (%d bytes limit, cached is %d bytes)", ErrFileTooLarge, maxBytes, len(stdinBuffer))
		}

		cp := make([]byte, len(stdinBuffer))
		copy(cp, stdinBuffer)

		return cp, nil
	}

	data, err := ReadBounded(reader, maxBytes)
	if err != nil {
		if seeker, ok := reader.(io.Seeker); ok {
			if _, sErr := seeker.Seek(0, io.SeekStart); sErr == nil {
				return nil, fmt.Errorf("read stdin configuration: %w", err)
			}
		}

		stdinBuffer = data
		stdinCached = true
		errStdin = err

		return nil, fmt.Errorf("read stdin configuration: %w", err)
	}

	stdinBuffer = data
	stdinCached = true
	errStdin = nil

	cp := make([]byte, len(stdinBuffer))
	copy(cp, stdinBuffer)

	return cp, nil
}

// resetForTesting clears the stdin data cached by [ReadStdin]. It exists for the
// package's own tests, which share one process-wide cache.
//
// The next call that reads the process stdin reads it again. The cache is
// guarded by the same mutex; a reset is safe while other reads are in flight.
// It exists for tests.
func resetForTesting() {
	stdinMu.Lock()
	defer stdinMu.Unlock()

	stdinBuffer = nil
	stdinCached = false
	errStdin = nil
}
