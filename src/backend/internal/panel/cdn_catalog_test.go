package panel

import (
	"context"
	"testing"
	"time"

	"lattix/backend/internal/panel/cdn"
	"lattix/backend/internal/panel/exchange"
	"lattix/backend/internal/panel/releases"
	"lattix/backend/internal/panel/scheduler"
)

func TestCoreTasksRegisterBackgroundCDNRefreshWithoutDNSInspection(t *testing.T) {
	panel := &Server{
		releases:  &releases.Catalog{},
		exchange:  &exchange.Catalog{},
		cdn:       &cdn.Catalog{},
		scheduler: scheduler.NewTaskScheduler(func(context.Context) *time.Location { return time.UTC }),
	}
	panel.registerCoreTasks()
	tasks := panel.scheduler.Snapshot()
	refresh, found := tasks["cdn.catalog.refresh"]
	if !found || refresh.RunOnStart {
		t.Fatalf("catalog refresh task = %+v, found=%v", refresh, found)
	}
	if _, found := tasks["cdn.dns.inspect"]; found {
		t.Fatal("DNS inspection task must not be registered")
	}
}
