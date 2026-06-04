package xfer

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"reflect"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/moby/go-archive/compression"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

// withFastDecompression enables the klauspost dispatcher and restores
// the previous value when the test ends.
func withFastDecompression(t *testing.T) {
	t.Helper()
	prev := decompressStream
	EnableFastDecompression(context.Background())
	t.Cleanup(func() { decompressStream = prev })
}

// resetDecompressStream forces the dispatcher back to the default for
// the duration of the test.
func resetDecompressStream(t *testing.T) {
	t.Helper()
	prev := decompressStream
	decompressStream = compression.DecompressStream
	t.Cleanup(func() { decompressStream = prev })
}

func TestDecompressStreamDefaultsToGoArchive(t *testing.T) {
	resetDecompressStream(t)
	got := reflect.ValueOf(decompressStream).Pointer()
	want := reflect.ValueOf(compression.DecompressStream).Pointer()
	assert.Equal(t, got, want)
}

func TestEnableFastDecompressionSwapsDispatcher(t *testing.T) {
	withFastDecompression(t)
	got := reflect.ValueOf(decompressStream).Pointer()
	want := reflect.ValueOf(fastDecompressStream).Pointer()
	assert.Equal(t, got, want)
}

// TestFastDecompressStreamRoundTrip covers the gzip path that this PR
// adds and two delegation paths (zstd, uncompressed) that must keep
// working unchanged.
func TestFastDecompressStreamRoundTrip(t *testing.T) {
	payload := bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 1024)

	tests := []struct {
		name string
		enc  func(t *testing.T, p []byte) []byte
	}{
		{
			name: "gzip",
			enc: func(t *testing.T, p []byte) []byte {
				var buf bytes.Buffer
				w := gzip.NewWriter(&buf)
				_, err := w.Write(p)
				assert.NilError(t, err)
				assert.NilError(t, w.Close())
				return buf.Bytes()
			},
		},
		{
			name: "zstd_delegation",
			enc: func(t *testing.T, p []byte) []byte {
				var buf bytes.Buffer
				w, err := kzstd.NewWriter(&buf)
				assert.NilError(t, err)
				_, err = w.Write(p)
				assert.NilError(t, err)
				assert.NilError(t, w.Close())
				return buf.Bytes()
			},
		},
		{
			name: "uncompressed_delegation",
			enc:  func(_ *testing.T, p []byte) []byte { return p },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := tc.enc(t, payload)
			rc, err := fastDecompressStream(bytes.NewReader(encoded))
			assert.NilError(t, err)
			defer rc.Close()

			got, err := io.ReadAll(rc)
			assert.NilError(t, err)
			assert.Assert(t, is.DeepEqual(payload, got))
		})
	}
}

// TestFastDecompressStreamMatchesDefault pins the safety guarantee that
// the dispatcher swap does not change layer digests.
func TestFastDecompressStreamMatchesDefault(t *testing.T) {
	payload := bytes.Repeat([]byte("docker layer payload bytes\n"), 4096)

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, err := w.Write(payload)
	assert.NilError(t, err)
	assert.NilError(t, w.Close())
	encoded := gz.Bytes()

	defaultRC, err := compression.DecompressStream(bytes.NewReader(encoded))
	assert.NilError(t, err)
	defer defaultRC.Close()
	defaultOut, err := io.ReadAll(defaultRC)
	assert.NilError(t, err)

	fastRC, err := fastDecompressStream(bytes.NewReader(encoded))
	assert.NilError(t, err)
	defer fastRC.Close()
	fastOut, err := io.ReadAll(fastRC)
	assert.NilError(t, err)

	assert.Assert(t, is.DeepEqual(defaultOut, fastOut))
}
