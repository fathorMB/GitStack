//go:build ignore

// check-browser-login prova il login come lo fa un browser su HTTP (GIT-153).
//
// curl e PowerShell rimandano anche un cookie Secure ricevuto su http; un
// browser (e net/http/cookiejar di Go) lo scarta, tranne che su localhost.
// La prova usa quindi un cookiejar e l'URL http://gitstack.test:8080 (nome
// non loopback), ma si collega sempre a -addr (127.0.0.1:8080) con un
// DialContext. L'Ingress del chart in CI non ha host: accetta qualsiasi Host.
//
// Passi: POST <prefix>/auth/login come admin, poi GET <prefix>/auth/session
// deve dare 200. Con gst_session sempre Secure il jar scarta il cookie e la
// seconda richiesta dà 401: il programma esce con 1.
//
// Uso: GITSTACK_PROBE_PASSWORD=... go run scripts/check-browser-login.go
//
//	[-addr 127.0.0.1:8080] [-host gitstack.test:8080] [-prefix /api/v1] [-user admin]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "indirizzo a cui connettersi davvero")
	host := flag.String("host", "gitstack.test:8080", "host dell'URL (non loopback)")
	prefix := flag.String("prefix", "/api/v1", "prefisso API")
	user := flag.String("user", "admin", "utente")
	flag.Parse()
	pw := os.Getenv("GITSTACK_PROBE_PASSWORD")
	if pw == "" {
		fail("GITSTACK_PROBE_PASSWORD vuota")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		fail("cookiejar: %v", err)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	client := &http.Client{
		Jar:     jar,
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, *addr)
			},
		},
	}
	base := "http://" + *host + *prefix

	body, _ := json.Marshal(map[string]string{"username": *user, "password": pw})
	resp, err := client.Post(base+"/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		fail("POST login: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("POST %s/auth/login: status %d, corpo %.200s", *prefix, resp.StatusCode, b)
	}
	for _, sc := range resp.Header.Values("Set-Cookie") {
		// Mai il valore del cookie: solo gli attributi.
		fmt.Printf("Set-Cookie ricevuto: %s\n", redact(sc))
	}
	u, _ := url.Parse(base)
	fmt.Printf("cookie nel jar per %s: %d\n", u.Host, len(jar.Cookies(u)))

	resp, err = client.Get(base + "/auth/session")
	if err != nil {
		fail("GET session: %v", err)
	}
	b, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("GET %s/auth/session dopo il login: status %d (atteso 200), corpo %.200s: il browser ha scartato il cookie?", *prefix, resp.StatusCode, b)
	}
	fmt.Printf("OK: login %s su http://%s e sessione 200 con cookie jar che rispetta Secure\n", *user, *host)
}

func redact(sc string) string {
	h := http.Header{}
	h.Add("Cookie", sc)
	req := http.Request{Header: h}
	cs := req.Cookies()
	if len(cs) == 0 {
		return sc
	}
	out := sc
	if cs[0].Value != "" {
		out = replaceOnce(sc, cs[0].Value, "<valore>")
	}
	return out
}

func replaceOnce(s, old, repl string) string {
	i := bytes.Index([]byte(s), []byte(old))
	if i < 0 {
		return s
	}
	return s[:i] + repl + s[i+len(old):]
}

func fail(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "FALLITO: "+format+"\n", a...)
	os.Exit(1)
}
