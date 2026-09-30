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
	DefaultMaxFileSize int64 = 1024 * 1024
)

var (
	ErrFileTooLarge = errors.New("configuration input exceeds maximum allowed size")

	stdinMu    sync.Mutex
	stdinCache struct {
		data  []byte
		ready bool
		err   error
	}
)

func ReadBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxFileSize
	}

	limit := maxBytes
	if maxBytes < math.MaxInt64 {
		// Read one byte past the bound to distinguish oversized input from a complete
		// document at the limit.
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

func ReadStdin(r io.Reader, maxBytes int64) ([]byte, error) {
	// Process stdin is read once and cached; supplied readers are read on every
	// call. Access to process stdin is serialized. Returned slices are caller-owned.
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

	if stdinCache.ready {
		if stdinCache.err != nil {
			return nil, fmt.Errorf("read stdin configuration: %w", stdinCache.err)
		}

		if int64(len(stdinCache.data)) > maxBytes {
			return nil, fmt.Errorf("%w (%d bytes limit, cached is %d bytes)", ErrFileTooLarge, maxBytes, len(stdinCache.data))
		}

		cp := make([]byte, len(stdinCache.data))
		copy(cp, stdinCache.data)

		return cp, nil
	}

	data, err := ReadBounded(reader, maxBytes)
	if err != nil {
		if seeker, ok := reader.(io.Seeker); ok {
			if _, seekErr := seeker.Seek(0, io.SeekStart); seekErr == nil {
				return nil, fmt.Errorf("read stdin configuration: %w", err)
			}
		}

		stdinCache.data = data
		stdinCache.ready = true
		stdinCache.err = err

		return nil, fmt.Errorf("read stdin configuration: %w", err)
	}

	stdinCache.data = data
	stdinCache.ready = true
	stdinCache.err = nil

	cp := make([]byte, len(stdinCache.data))
	copy(cp, stdinCache.data)

	return cp, nil
}

func resetForTesting() {
	stdinMu.Lock()
	defer stdinMu.Unlock()

	stdinCache.data = nil
	stdinCache.ready = false
	stdinCache.err = nil
}
