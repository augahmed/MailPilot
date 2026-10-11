package app

import (
	"context"
	"errors"
	"testing"
)

type probe struct{ err error }

func (p probe) Ping(context.Context) error { return p.err }
func (probe) SchemaVersion() int           { return 1 }
func TestHealthReportsDatabaseFailure(t *testing.T) {
	health := New(probe{err: errors.New("unavailable")}, "test").Health(context.Background())
	if health.Database != "disconnected" || health.Backend != "connected" || health.Version != "test" {
		t.Fatalf("incorrect health: %+v", health)
	}
}
