package outbox

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFillUserTypes(t *testing.T) {
	bot, human := uuid.New(), uuid.New()
	payload := []byte(`{"repo":{"id":"r"},"actor":{"id":"` + human.String() + `","username":"ada"},` +
		`"assignee":{"id":"` + bot.String() + `","username":"bot"},"issue":{"id":"i"}}`)
	out, err := fillUserTypes(payload, map[uuid.UUID]string{bot: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Actor, Assignee struct{ ID, Username, Type string }
		Issue           struct{ ID string }
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Actor.Type != "human" || got.Assignee.Type != "agent" || got.Actor.Username != "ada" || got.Issue.ID != "i" {
		t.Fatalf("payload = %s", out)
	}
	// Un tipo già presente non si tocca; actor null resta null.
	same, err := fillUserTypes([]byte(`{"actor":null,"assignee":{"id":"`+bot.String()+`","username":"x","type":"human"}}`), map[uuid.UUID]string{bot: "agent"})
	if err != nil || string(same) != `{"actor":null,"assignee":{"id":"`+bot.String()+`","username":"x","type":"human"}}` {
		t.Fatalf("payload = %s, err = %v", same, err)
	}
}

func TestDelayRaddoppiaFinoAlMassimo(t *testing.T) {
	r := &Relay{BaseDelay: time.Second, MaxDelay: 10 * time.Second}
	want := []time.Duration{1, 2, 4, 8, 10, 10}
	for i, w := range want {
		if got := r.delay(i); got != w*time.Second {
			t.Errorf("delay(%d) = %v, voluto %v", i, got, w*time.Second)
		}
	}
}
