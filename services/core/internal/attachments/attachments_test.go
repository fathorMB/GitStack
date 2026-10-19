package attachments

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDiskSaveRawCalcolaDigestESalvaBinario(t *testing.T) {
	d := &Disk{Dir: t.TempDir()}
	repo, id := uuid.New(), uuid.New()
	b := []byte{0, 1, 2, 255, 0, 3}
	n, got, err := d.SaveRaw(repo, id, bytes.NewReader(b), 16)
	if err != nil || n != int64(len(b)) {
		t.Fatalf("SaveRaw: n=%d err=%v", n, err)
	}
	want := sha256.Sum256(b)
	if got != hex.EncodeToString(want[:]) {
		t.Fatalf("digest=%s want=%x", got, want)
	}
	f, err := d.Open(repo, id)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gotBytes, err := io.ReadAll(f)
	if err != nil || !bytes.Equal(gotBytes, b) {
		t.Fatalf("file=%x err=%v", gotBytes, err)
	}
}

func TestDetect(t *testing.T) {
	ok := map[string][]byte{
		TypePNG:  []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		TypeJPEG: []byte("\xff\xd8\xff\xe0\x00\x10JFIF"),
		TypeGIF:  []byte("GIF89a\x01\x00\x01\x00"),
		TypeWebP: []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		TypePDF:  []byte("%PDF-1.4\n"),
		TypeZIP:  []byte("PK\x03\x04\x14\x00"),
		TypeText: []byte("2026-10-05 12:00 INFO avviato\n"),
	}
	for want, b := range ok {
		got, err := Detect(b)
		if err != nil || got != want {
			t.Errorf("%q: %q, %v (voluto %q)", b, got, err, want)
		}
	}
	bad := map[string][]byte{
		"vuoto":     {},
		"html":      []byte("<!DOCTYPE html><html>"),
		"html_s":    []byte("  \n<html><body>"),
		"script":    []byte("<script>alert(1)</script>"),
		"svg":       []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"svg_xml":   []byte(`<?xml version="1.0"?><svg/>`),
		"svg_dopo":  []byte("testo qualunque <svg onload=x>"),
		"exe":       append([]byte("MZ"), bytes.Repeat([]byte{0, 1, 2}, 30)...),
		"binario":   bytes.Repeat([]byte{0, 0xff, 0x80}, 40),
		"bom_html":  []byte("\xef\xbb\xbf<html>"),
		"markup_pl": []byte("<?php echo 1; ?>"),
	}
	for name, b := range bad {
		if got, err := Detect(b); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: ammesso come %q", name, got)
		}
	}
}

func TestDisk_SaveLimiteERimozione(t *testing.T) {
	d := &Disk{Dir: t.TempDir()}
	repo, id := uuid.New(), uuid.New()

	// Al limite: ammesso. Un byte oltre: ErrTooLarge e niente file.
	body := strings.Repeat("a", 2000)
	ct, n, err := d.Save(repo, id, strings.NewReader(body), 2000)
	if err != nil || n != 2000 || ct != TypeText {
		t.Fatalf("al limite: %q %d %v", ct, n, err)
	}
	if _, _, err := d.Save(repo, uuid.New(), strings.NewReader(body+"a"), 2000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oltre il limite: %v", err)
	}
	// Un file piccolo oltre il limite dentro i primi 512 byte.
	if _, _, err := d.Save(repo, uuid.New(), strings.NewReader("abcdef"), 3); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("piccolo oltre il limite: %v", err)
	}
	if _, _, err := d.Save(repo, uuid.New(), strings.NewReader("<html>"), 100); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("html: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(d.Dir, repo.String()))
	if len(entries) != 1 || entries[0].Name() != id.String() {
		t.Errorf("sul volume: %v", entries)
	}

	if err := d.Remove(repo, id); err != nil {
		t.Fatal(err)
	}
	if err := d.Remove(repo, id); err != nil { // già assente
		t.Fatal(err)
	}
	_, _, _ = d.Save(repo, uuid.New(), strings.NewReader("x"), 10)
	if err := d.RemoveRepo(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.Dir, repo.String())); !os.IsNotExist(err) {
		t.Errorf("cartella del repo rimasta: %v", err)
	}
}
