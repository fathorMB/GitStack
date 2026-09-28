package events

import (
	"context"
	"log/slog"
	"testing"
)

func TestNoopPublisher_DoesNotError(t *testing.T) {
	p := NoopPublisher{Logger: slog.Default()}

	err := p.Publish(context.Background(), TestResourceCreatedName, TestResourceCreatedVersion, TestResourceCreatedPayload{
		ResourceID: "11111111-1111-1111-1111-111111111111",
		Type:       "repo",
		Name:       "gitstack",
	})
	if err != nil {
		t.Fatalf("NoopPublisher.Publish non deve fallire: %v", err)
	}
}

func TestNoopPublisher_NilLoggerDoesNotPanic(t *testing.T) {
	p := NoopPublisher{}
	if err := p.Publish(context.Background(), TestResourceCreatedName, TestResourceCreatedVersion, nil); err != nil {
		t.Fatalf("NoopPublisher.Publish non deve fallire: %v", err)
	}
}
