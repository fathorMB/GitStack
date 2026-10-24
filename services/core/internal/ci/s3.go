package ci

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// S3Config configura S3Blobs. Endpoint è l'indirizzo base dello storage compatibile S3
// (https://s3.example.org); gli oggetti stanno in <Endpoint>/<Bucket>/<Prefix><key> (path-style).
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	Prefix    string
	AccessKey string
	SecretKey string
	// SpoolDir è dove si appoggia l'oggetto prima del caricamento (serve conoscere dimensione
	// e SHA-256 firmato); vuoto = cartella temporanea di sistema.
	SpoolDir string
	// Client sostituisce http.DefaultClient (test, proxy).
	Client *http.Client
	Now    func() time.Time
}

// S3Blobs archivia gli oggetti su un bucket S3 con richieste firmate SigV4.
type S3Blobs struct{ cfg S3Config }

// NewS3Blobs controlla la configurazione e costruisce l'archivio.
func NewS3Blobs(cfg S3Config) (*S3Blobs, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("endpoint S3 non valido: %q", cfg.Endpoint)
	}
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("S3: servono bucket, access key e secret key")
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Minute}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	return &S3Blobs{cfg: cfg}, nil
}

func (s *S3Blobs) objectURL(key string) (*url.URL, error) {
	if err := validBlobKey(key); err != nil {
		return nil, err
	}
	u, err := url.Parse(s.cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	u.Path = "/" + s.cfg.Bucket + "/" + s.cfg.Prefix + key
	u.RawPath = ""
	return u, nil
}

// escapePath codifica i segmenti come richiede la firma SigV4 per S3 (una volta sola).
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
	}
	return strings.Join(segs, "/")
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	_, _ = m.Write([]byte(data))
	return m.Sum(nil)
}

// sign aggiunge Authorization e le intestazioni x-amz-* alla richiesta (SigV4, servizio s3).
func (s *S3Blobs) sign(req *http.Request, payloadHash string) {
	now := s.cfg.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	canonicalURI := escapePath(req.URL.Path)
	signed := "host;x-amz-content-sha256;x-amz-date"
	canonical := strings.Join([]string{
		req.Method, canonicalURI, req.URL.RawQuery,
		"host:" + req.URL.Host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + amzDate + "\n",
		signed, payloadHash,
	}, "\n")
	scope := day + "/" + s.cfg.Region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := hmacSHA256([]byte("AWS4"+s.cfg.SecretKey), day)
	k = hmacSHA256(k, s.cfg.Region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+s.cfg.AccessKey+"/"+scope+
		", SignedHeaders="+signed+", Signature="+sig)
}

func (s *S3Blobs) Put(ctx context.Context, key string, r io.Reader, max int64) (int64, error) {
	u, err := s.objectURL(key)
	if err != nil {
		return 0, err
	}
	spool, err := os.CreateTemp(s.cfg.SpoolDir, "ci-s3-put-*")
	if err != nil {
		return 0, err
	}
	defer func() { _ = spool.Close(); _ = os.Remove(spool.Name()) }()
	h := sha256.New()
	n, err := copyLimited(ctx, io.MultiWriter(spool, h), r, max)
	if err != nil {
		return 0, err
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), spool)
	if err != nil {
		return 0, err
	}
	req.ContentLength = n
	if n == 0 {
		req.Body = http.NoBody
	}
	s.sign(req, hex.EncodeToString(h.Sum(nil)))
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("S3 PUT: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	return n, nil
}

func (s *S3Blobs) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	u, err := s.objectURL(key)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	s.sign(req, emptySHA256)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusNotFound {
		_ = resp.Body.Close()
		return nil, ErrBlobNotFound
	}
	if resp.StatusCode/100 != 2 {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("S3 GET: %s", resp.Status)
	}
	return resp.Body, nil
}

func (s *S3Blobs) Delete(ctx context.Context, key string) error {
	u, err := s.objectURL(key)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	s.sign(req, emptySHA256)
	resp, err := s.cfg.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("S3 DELETE: %s", resp.Status)
	}
	return nil
}

// emptySHA256 è lo SHA-256 del corpo vuoto.
const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
