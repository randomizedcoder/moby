package xfer

import (
	"bufio"
	"context"
	"errors"
	"io"
	"sync"

	"github.com/containerd/log"
	kgzip "github.com/klauspost/compress/gzip"
	"github.com/moby/go-archive/compression"
)

// decompressStream is the decompressor used by the layer download path.
// EnableFastDecompression swaps it at daemon startup; the convention
// matches graphdriver.ApplyUncompressedLayer.
var decompressStream = compression.DecompressStream

// EnableFastDecompression replaces the gzip reader on the layer-pull
// path with klauspost/compress/gzip. Must be called before any pull
// begins.
func EnableFastDecompression(ctx context.Context) {
	log.G(ctx).Info("(Experimental) fast-decompression enabled; using klauspost/compress/gzip for layer decode")
	decompressStream = fastDecompressStream
}

var bufReaderPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(nil, 32*1024) },
}

func releaseBufReader(br *bufio.Reader) {
	br.Reset(nil)
	bufReaderPool.Put(br)
}

// fastDecompressStream intercepts gzip and delegates other formats to
// compression.DecompressStream.
func fastDecompressStream(archive io.Reader) (io.ReadCloser, error) {
	br := bufReaderPool.Get().(*bufio.Reader)
	br.Reset(archive)
	bs, err := br.Peek(10)
	if err != nil && !errors.Is(err, io.EOF) {
		// Issue 18170: empty layer.tar produces io.EOF; treat as None.
		releaseBufReader(br)
		return nil, err
	}

	if compression.Detect(bs) == compression.Gzip {
		gz, err := kgzip.NewReader(br)
		if err != nil {
			releaseBufReader(br)
			return nil, err
		}
		return &pooledReadCloser{ReadCloser: gz, buf: br}, nil
	}

	rc, err := compression.DecompressStream(br)
	if err != nil {
		releaseBufReader(br)
		return nil, err
	}
	return &pooledReadCloser{ReadCloser: rc, buf: br}, nil
}

// pooledReadCloser returns the buffered reader to the pool on Close.
type pooledReadCloser struct {
	io.ReadCloser
	buf *bufio.Reader
}

func (p *pooledReadCloser) Close() error {
	err := p.ReadCloser.Close()
	if p.buf != nil {
		releaseBufReader(p.buf)
		p.buf = nil
	}
	return err
}
