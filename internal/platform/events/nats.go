package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bengobox/subscription-service/internal/config"
)

// Connect establishes a NATS connection with sane defaults.
func Connect(cfg config.EventsConfig) (*nats.Conn, error) {
	opts := []nats.Option{
		nats.Name("subscription-service"),
		nats.Timeout(5 * time.Second),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
	}

	return nats.Connect(cfg.NATSURL, opts...)
}

// RequiredSubjects is the full set of subject wildcards this service's outbox
// publisher must be able to durably publish to. Every aggregate_type this
// service ever writes to outbox_events needs its wildcard listed here —
// EnsureStream uses this both to seed a brand-new stream and to self-heal an
// already-existing one that predates a later addition (e.g. "email.>" was
// added after "subscription.>" was already live in production; a stream that
// exists but doesn't cover a subject silently black-holes every publish for
// it — see the 2026-08-19 email-license outbox bug this fixed).
var RequiredSubjects = []string{"subscription.>", "email.>"}

// EnsureStream creates the JetStream stream for this service's outbox events
// if it doesn't exist, and — critically — extends an already-existing
// stream's subjects if RequiredSubjects has grown since it was created.
// js.AddStream is only ever called once per stream's lifetime; every
// subsequent boot must go through the update path below, or a subject added
// to RequiredSubjects later has no effect on a live stream.
func EnsureStream(ctx context.Context, nc *nats.Conn, cfg config.EventsConfig) error {
	if nc == nil {
		return fmt.Errorf("nats connection is nil")
	}

	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream init: %w", err)
	}

	info, err := js.StreamInfo(cfg.StreamName)
	if err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:     cfg.StreamName,
			Subjects: RequiredSubjects,
			Replicas: 1,
		})
		if err != nil {
			return fmt.Errorf("create stream %s: %w", cfg.StreamName, err)
		}
		return nil
	}

	missing := missingSubjects(info.Config.Subjects, RequiredSubjects)
	if len(missing) == 0 {
		return nil
	}

	updated := info.Config
	updated.Subjects = append(append([]string{}, info.Config.Subjects...), missing...)
	if _, err := js.UpdateStream(&updated); err != nil {
		return fmt.Errorf("extend stream %s subjects to include %v: %w", cfg.StreamName, missing, err)
	}
	return nil
}

// missingSubjects returns the entries of want not already covered by any
// wildcard in existing (a subject is "covered" if it's already present
// verbatim, or existing already has a "<prefix>.>" that would match it).
func missingSubjects(existing, want []string) []string {
	var missing []string
	for _, w := range want {
		if !subjectCovered(existing, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

func subjectCovered(existing []string, want string) bool {
	for _, e := range existing {
		if e == want {
			return true
		}
		if strings.HasSuffix(e, ".>") && strings.HasPrefix(want, strings.TrimSuffix(e, ">")) {
			return true
		}
	}
	return false
}

