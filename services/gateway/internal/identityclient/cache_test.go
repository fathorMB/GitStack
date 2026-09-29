package identityclient

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

type fakeVerifier struct {
	res   Result
	err   error
	calls int
}

func (f *fakeVerifier) Verify(context.Context, string, Kind) (Result, error) {
	f.calls++
	return f.res, f.err
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestCache(f *fakeVerifier) (*Cache, *fakeClock) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewCache(f, DefaultPositiveTTL, DefaultNegativeTTL, clk.now), clk
}

func TestCache_PositivoTrentaSecondi(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: true, Principal: Caller{Username: "alice"}}}
	c, clk := newTestCache(f)

	for i := 0; i < 3; i++ {
		res, err := c.Verify(context.Background(), "gst_abc", KindToken)
		if err != nil || !res.Active || res.Principal.Username != "alice" {
			t.Fatalf("Verify = %+v %v", res, err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("chiamate a identity = %d, volevo 1 (le altre dalla cache)", f.calls)
	}
	clk.t = clk.t.Add(29 * time.Second)
	_, _ = c.Verify(context.Background(), "gst_abc", KindToken)
	if f.calls != 1 {
		t.Fatalf("a 29 s la voce deve valere ancora: chiamate = %d", f.calls)
	}
	clk.t = clk.t.Add(time.Second) // 30 s esatti: scaduta
	_, _ = c.Verify(context.Background(), "gst_abc", KindToken)
	if f.calls != 2 {
		t.Fatalf("a 30 s la voce è scaduta: chiamate = %d, volevo 2", f.calls)
	}
}

func TestCache_NegativoCinqueSecondi(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: false}}
	c, clk := newTestCache(f)

	for i := 0; i < 2; i++ {
		if res, err := c.Verify(context.Background(), "inventata", KindSession); err != nil || res.Active {
			t.Fatalf("Verify = %+v %v", res, err)
		}
	}
	if f.calls != 1 {
		t.Fatalf("chiamate = %d, volevo 1", f.calls)
	}
	clk.t = clk.t.Add(5 * time.Second)
	_, _ = c.Verify(context.Background(), "inventata", KindSession)
	if f.calls != 2 {
		t.Fatalf("a 5 s l'esito negativo è scaduto: chiamate = %d", f.calls)
	}
}

func TestCache_MaiOltreExpiresAt(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	exp := clk.t.Add(10 * time.Second)
	f := &fakeVerifier{res: Result{Active: true, Principal: Caller{ExpiresAt: &exp}}}
	c := NewCache(f, DefaultPositiveTTL, DefaultNegativeTTL, clk.now)

	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	clk.t = clk.t.Add(9 * time.Second)
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	if f.calls != 1 {
		t.Fatalf("prima di expiresAt la voce vale: chiamate = %d", f.calls)
	}
	clk.t = clk.t.Add(time.Second) // = expiresAt
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	if f.calls != 2 {
		t.Fatalf("a expiresAt la voce non vale più: chiamate = %d", f.calls)
	}

	// Già scaduta al momento della verifica: non si mette in cache.
	past := clk.t.Add(-time.Second)
	f.res = Result{Active: true, Principal: Caller{ExpiresAt: &past}}
	before := f.calls
	_, _ = c.Verify(context.Background(), "gst_y", KindToken)
	_, _ = c.Verify(context.Background(), "gst_y", KindToken)
	if f.calls != before+2 {
		t.Fatalf("una credenziale già scaduta non va in cache: chiamate = %d", f.calls-before)
	}
}

func TestCache_RispettaIlTTLDiIdentityPiuBreve(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: true, TTL: 2 * time.Second}}
	c, clk := newTestCache(f)
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	clk.t = clk.t.Add(2 * time.Second)
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	if f.calls != 2 {
		t.Fatalf("TTL di identity di 2 s: chiamate = %d, volevo 2", f.calls)
	}

	// Un TTL suggerito più lungo di quello del gateway non lo allunga.
	f2 := &fakeVerifier{res: Result{Active: true, TTL: time.Hour}}
	c2, clk2 := newTestCache(f2)
	_, _ = c2.Verify(context.Background(), "gst_x", KindToken)
	clk2.t = clk2.t.Add(30 * time.Second)
	_, _ = c2.Verify(context.Background(), "gst_x", KindToken)
	if f2.calls != 2 {
		t.Fatalf("il TTL di identity non allunga quello del gateway: chiamate = %d", f2.calls)
	}
}

// Identity irraggiungibile: si serve ciò che è ancora nel TTL, ma la voce
// scaduta non si riusa né si allunga, e si ritorna l'errore (503): mai fail
// open.
func TestCache_IdentityGiu(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: true, Principal: Caller{Username: "alice"}}}
	c, clk := newTestCache(f)
	if _, err := c.Verify(context.Background(), "gst_x", KindToken); err != nil {
		t.Fatal(err)
	}

	f.err = ErrUnavailable
	clk.t = clk.t.Add(20 * time.Second)
	if res, err := c.Verify(context.Background(), "gst_x", KindToken); err != nil || !res.Active {
		t.Fatalf("dentro il TTL con identity giù si serve dalla cache: %+v %v", res, err)
	}
	clk.t = clk.t.Add(10 * time.Second) // 30 s dalla verifica
	if res, err := c.Verify(context.Background(), "gst_x", KindToken); !errors.Is(err, ErrUnavailable) || res.Active {
		t.Fatalf("oltre il TTL con identity giù deve essere ErrUnavailable senza principal: %+v %v", res, err)
	}
	// e non è stato "allungato": ancora errore alla richiesta dopo.
	clk.t = clk.t.Add(time.Second)
	if _, err := c.Verify(context.Background(), "gst_x", KindToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
	// Una credenziale mai vista con identity giù: errore, non "non attiva".
	if _, err := c.Verify(context.Background(), "gst_nuova", KindToken); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestCache_ErroriNonInCache(t *testing.T) {
	f := &fakeVerifier{err: ErrUnavailable}
	c, _ := newTestCache(f)
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	f.err = nil
	f.res = Result{Active: true}
	if res, err := c.Verify(context.Background(), "gst_x", KindToken); err != nil || !res.Active {
		t.Fatalf("dopo un errore la richiesta successiva riprova: %+v %v", res, err)
	}
}

// La chiave è lo SHA-256 della credenziale: il valore in chiaro non sta
// nella cache.
func TestCache_ChiaveSha256(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: true}}
	c, _ := newTestCache(f)
	_, _ = c.Verify(context.Background(), "gst_segretissimo", KindToken)

	if len(c.entries) != 1 {
		t.Fatalf("voci = %d", len(c.entries))
	}
	if _, ok := c.entries[Key("gst_segretissimo")]; !ok {
		t.Fatal("la voce non è indicizzata per SHA-256 della credenziale")
	}
	// SHA-256("abc"), vettore noto.
	const abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	got := Key("abc")
	if hex := toHex(got[:]); hex != abc {
		t.Fatalf("Key(abc) = %s, voluto %s", hex, abc)
	}
}

func toHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&0xf])
	}
	return string(out)
}

func TestCache_Forget(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: true}}
	c, _ := newTestCache(f)
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	c.Forget("gst_x")
	_, _ = c.Verify(context.Background(), "gst_x", KindToken)
	if f.calls != 2 {
		t.Fatalf("dopo Forget si richiede a identity: chiamate = %d", f.calls)
	}
}

func TestCache_Limite(t *testing.T) {
	f := &fakeVerifier{res: Result{Active: false}}
	c, _ := newTestCache(f)
	for i := 0; i < maxEntries+50; i++ {
		_, _ = c.Verify(context.Background(), "cred-"+strconv.Itoa(i), KindSession)
	}
	if len(c.entries) > maxEntries {
		t.Fatalf("voci = %d, oltre il limite %d", len(c.entries), maxEntries)
	}
}
