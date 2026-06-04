package xfer

import (
	"bytes"
	"compress/gzip"
	"io"
	"math/rand"
	"os"
	"testing"

	"github.com/docker/go-units"
	"github.com/moby/go-archive/compression"
)

// BenchmarkDecompressStream compares the default gzip decoder against
// the klauspost-backed fastDecompressStream. Opt-in via -bench. Payload
// size from MOBY_BENCH_PAYLOAD_SIZE (default 10 MiB,
// units.FromHumanSize syntax).
//
//	go test -bench=BenchmarkDecompressStream -benchmem -run=^$ \
//	    ./daemon/internal/distribution/xfer/
func BenchmarkDecompressStream(b *testing.B) {
	size := benchPayloadSize(b)
	payload := benchPayload(size)
	encoded := encodeGzipForBench(b, payload)
	ratio := float64(len(encoded)) * 100 / float64(len(payload))
	b.Logf("gzip: %d bytes payload compressed to %d bytes (%.1f%% ratio)",
		len(payload), len(encoded), ratio)

	impls := []struct {
		name string
		fn   func(io.Reader) (io.ReadCloser, error)
	}{
		{"default", compression.DecompressStream},
		{"klauspost", fastDecompressStream},
	}

	for _, impl := range impls {
		b.Run("gzip/"+impl.name, func(b *testing.B) {
			b.SetBytes(int64(len(payload)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				rc, err := impl.fn(bytes.NewReader(encoded))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, rc); err != nil {
					b.Fatal(err)
				}
				_ = rc.Close()
			}
		})
	}
}

func benchPayloadSize(b *testing.B) int {
	s := os.Getenv("MOBY_BENCH_PAYLOAD_SIZE")
	if s == "" {
		return 10 << 20
	}
	n, err := units.FromHumanSize(s)
	if err != nil || n <= 0 {
		b.Fatalf("invalid MOBY_BENCH_PAYLOAD_SIZE %q: %v", s, err)
	}
	return int(n)
}

// benchPayload returns deterministic bytes shaped roughly like a real
// container layer: structured text (compresses well) interleaved with
// pseudo-random noise (does not).
func benchPayload(size int) []byte {
	const block = "moby docker container layer payload " +
		"the quick brown fox jumps over the lazy dog " +
		"package metadata config file source listing\n"
	out := make([]byte, 0, size)
	rng := rand.New(rand.NewSource(1))
	for len(out) < size {
		textChunk := 16 << 10
		noiseChunk := 4 << 10
		for textChunk > 0 && len(out) < size {
			n := min(textChunk, size-len(out))
			for i := 0; i < n; {
				w := copy(out[len(out):len(out)+min(n-i, len(block))], block)
				out = out[:len(out)+w]
				i += w
			}
			textChunk -= n
		}
		if len(out) >= size {
			break
		}
		noise := make([]byte, min(noiseChunk, size-len(out)))
		_, _ = rng.Read(noise)
		out = append(out, noise...)
	}
	return out[:size]
}

func encodeGzipForBench(b *testing.B, p []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(p); err != nil {
		b.Fatal(err)
	}
	if err := w.Close(); err != nil {
		b.Fatal(err)
	}
	return buf.Bytes()
}
