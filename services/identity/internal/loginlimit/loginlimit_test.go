package loginlimit

import (
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)} }

func TestBlocksAfterNAndUnblocksAfterWindow(t *testing.T) {
	c := newClock()
	l := New(Config{MaxPerUser: 3, MaxPerIP: 100, Window: 10 * time.Minute}, c.now)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Check("Alice", "1.1.1.1"); !ok {
			t.Fatalf("tentativo %d bloccato troppo presto", i)
		}
		l.Fail("alice", "1.1.1.1")
		c.advance(time.Minute)
	}
	ok, retry := l.Check("ALICE", "9.9.9.9") // case-insensitive, altro IP
	if ok {
		t.Fatal("dopo 3 fallimenti l'utente deve essere bloccato")
	}
	// Il primo fallimento era a t0, ora è t0+3m: sblocco a t0+10m => 7m.
	if retry != 7*time.Minute {
		t.Fatalf("retryAfter = %v, atteso 7m", retry)
	}
	c.advance(retry)
	if ok, _ := l.Check("alice", "9.9.9.9"); !ok {
		t.Fatal("dopo la finestra l'utente deve essere sbloccato")
	}
}

func TestPerIPLimit(t *testing.T) {
	c := newClock()
	l := New(Config{MaxPerUser: 100, MaxPerIP: 2, Window: time.Minute}, c.now)
	l.Fail("a", "2.2.2.2")
	l.Fail("b", "2.2.2.2")
	if ok, _ := l.Check("c", "2.2.2.2"); ok {
		t.Fatal("l'IP con 2 fallimenti deve essere bloccato anche per un altro utente")
	}
	if ok, _ := l.Check("c", "3.3.3.3"); !ok {
		t.Fatal("un altro IP non deve essere bloccato")
	}
	c.advance(time.Minute)
	if ok, _ := l.Check("c", "2.2.2.2"); !ok {
		t.Fatal("IP non sbloccato dopo la finestra")
	}
}

func TestSuccessResetsUserOnly(t *testing.T) {
	c := newClock()
	l := New(Config{MaxPerUser: 2, MaxPerIP: 3, Window: time.Hour}, c.now)
	l.Fail("a", "1.1.1.1")
	l.Success("a")
	l.Fail("a", "1.1.1.1")
	if ok, _ := l.Check("a", "8.8.8.8"); !ok {
		t.Fatal("il successo doveva azzerare il contatore dell'utente")
	}
	l.Fail("b", "1.1.1.1")
	if ok, _ := l.Check("z", "1.1.1.1"); ok {
		t.Fatal("il contatore dell'IP non si azzera col successo di un utente")
	}
}

func TestFromEnv(t *testing.T) {
	env := map[string]string{
		"GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER": "7",
		"GITSTACK_IDENTITY_LOGIN_WINDOW":            "30m",
	}
	c, err := FromEnv(func(k string) string { return env[k] })
	if err != nil || c.MaxPerUser != 7 || c.MaxPerIP != Default.MaxPerIP || c.Window != 30*time.Minute {
		t.Fatalf("FromEnv = %+v, %v", c, err)
	}
	env["GITSTACK_IDENTITY_LOGIN_WINDOW"] = "boh"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("durata non valida accettata")
	}
	env["GITSTACK_IDENTITY_LOGIN_WINDOW"] = ""
	env["GITSTACK_IDENTITY_LOGIN_MAX_ATTEMPTS_USER"] = "0"
	if _, err := FromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("N=0 accettato")
	}
}
