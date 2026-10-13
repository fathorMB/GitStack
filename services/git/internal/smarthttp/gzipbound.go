package smarthttp

import (
	"compress/gzip"
	"errors"
	"io"
)

// Tetto alla decompressione del corpo gzip di una richiesta: non è un tetto
// sul push (il pack di un push grande viaggia non compresso e git lo limita
// con le regole di ricezione), ma sul rapporto fra byte decompressi e byte
// compressi realmente ricevuti, più una soglia fissa per le richieste piccole
// (le negoziazioni want/have di git).
const (
	// DefaultGzipFloor è la quota di byte decompressi sempre concessa.
	DefaultGzipFloor int64 = 32 << 20
	// DefaultGzipRatio è il massimo rapporto decompresso/compresso oltre la
	// soglia: il testo pkt-line di git comprime ben sotto 100:1, una gzip
	// bomb (zeri) sta oltre 1000:1.
	DefaultGzipRatio int64 = 100
)

// ErrGzipTooLarge: il corpo gzip decompresso supera il tetto.
var ErrGzipTooLarge = errors.New("smarthttp: corpo gzip decompresso oltre il limite")

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// boundedGzip decomprime in streaming e interrompe con ErrGzipTooLarge
// quando i byte decompressi superano floor + ratio*byte compressi letti.
type boundedGzip struct {
	zr         *gzip.Reader
	in         *countingReader
	floor, rat int64
	out        int64
	onExceed   func()
}

// NewBoundedGzip apre un lettore gzip su r con il tetto di decompressione;
// floor e ratio <= 0 usano i default. onExceed (opzionale) è chiamata una
// volta al superamento.
func NewBoundedGzip(r io.Reader, floor, ratio int64, onExceed func()) (io.ReadCloser, error) {
	if floor <= 0 {
		floor = DefaultGzipFloor
	}
	if ratio <= 0 {
		ratio = DefaultGzipRatio
	}
	in := &countingReader{r: r}
	zr, err := gzip.NewReader(in)
	if err != nil {
		return nil, err
	}
	return &boundedGzip{zr: zr, in: in, floor: floor, rat: ratio, onExceed: onExceed}, nil
}

func (b *boundedGzip) Read(p []byte) (int, error) {
	n, err := b.zr.Read(p)
	b.out += int64(n)
	if b.out > b.floor+b.rat*b.in.n {
		if b.onExceed != nil {
			b.onExceed()
			b.onExceed = nil
		}
		return 0, ErrGzipTooLarge
	}
	return n, err
}

func (b *boundedGzip) Close() error { return b.zr.Close() }
