package stats

import (
	"context"
	"testing"
	"time"
)

// latestNow is the clock of these tests: noon UTC on 2024-12-10.
var latestNow = time.Date(2024, 12, 10, 12, 0, 0, 0, time.UTC)

func TestLatestDataDay(t *testing.T) {
	ctx := context.Background()

	t.Run("empty database", func(t *testing.T) {
		conn := newDB(t)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || found || day != "" {
			t.Errorf("got %q, %v, %v; want no day", day, found, err)
		}
	})

	t.Run("metric day", func(t *testing.T) {
		conn := newDB(t)
		addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || !found || day != "2024-12-05" {
			t.Errorf("got %q, %v, %v; want 2024-12-05", day, found, err)
		}
	})

	t.Run("workout day beats an older metric day", func(t *testing.T) {
		conn := newDB(t)
		addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
		addWorkout(t, conn, "running", "2024-12-08 07:00 +0000", "2024-12-08 08:00 +0000", watch)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || !found || day != "2024-12-08" {
			t.Errorf("got %q, %v, %v; want 2024-12-08", day, found, err)
		}
	})

	t.Run("sleep night ending after 18:00 belongs to the next day", func(t *testing.T) {
		conn := newDB(t)
		addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
		// Ends 19:00 on the 8th; with the 6-hour shift that is the 9th.
		addSleep(t, conn, "core", "2024-12-08 18:00 +0000", "2024-12-08 19:00 +0000", watch)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || !found || day != "2024-12-09" {
			t.Errorf("got %q, %v, %v; want 2024-12-09", day, found, err)
		}
	})

	t.Run("evening nap today is not tomorrow", func(t *testing.T) {
		conn := newDB(t)
		addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
		// Starts before now + 24 h, but the shift dates it the 11th: left out.
		addSleep(t, conn, "core", "2024-12-10 19:00 +0000", "2024-12-10 20:00 +0000", watch)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || !found || day != "2024-12-05" {
			t.Errorf("got %q, %v, %v; want 2024-12-05", day, found, err)
		}
	})

	t.Run("future-dated sample is ignored", func(t *testing.T) {
		conn := newDB(t)
		addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
		addSample(t, conn, "step_count", "2024-12-20 09:00 +0000", 1000, watch)
		addWorkout(t, conn, "running", "2024-12-25 07:00 +0000", "2024-12-25 08:00 +0000", watch)
		day, found, err := LatestDataDay(ctx, conn, latestNow)
		if err != nil || !found || day != "2024-12-05" {
			t.Errorf("got %q, %v, %v; want 2024-12-05", day, found, err)
		}
	})
}

func TestLatestDayFor(t *testing.T) {
	ctx := context.Background()
	conn := newDB(t)

	if _, found, err := LatestDayFor(ctx, conn, "steps"); err != nil || found {
		t.Errorf("empty steps: found %v, err %v", found, err)
	}
	if _, found, err := LatestDayFor(ctx, conn, "sleep"); err != nil || found {
		t.Errorf("empty sleep: found %v, err %v", found, err)
	}

	addSample(t, conn, "step_count", "2024-12-05 09:00 +0000", 1000, watch)
	addSleep(t, conn, "core", "2024-12-08 18:00 +0000", "2024-12-08 19:00 +0000", watch)
	// In-bed rows alone are not sleep.
	addSleep(t, conn, "in_bed", "2024-12-09 22:00 +0000", "2024-12-10 06:00 +0000", iphone)

	if day, found, err := LatestDayFor(ctx, conn, "steps"); err != nil || !found || day != "2024-12-05" {
		t.Errorf("steps: %q, %v, %v", day, found, err)
	}
	if day, found, err := LatestDayFor(ctx, conn, "sleep"); err != nil || !found || day != "2024-12-09" {
		t.Errorf("sleep: %q, %v, %v", day, found, err)
	}
}

func TestLatestSample(t *testing.T) {
	conn := newDB(t)
	addSample(t, conn, "body_mass", "2024-12-01 07:00 +0100", 79.0, watch)
	addSample(t, conn, "body_mass", "2024-12-04 07:00 -0500", 78.0, "manual")
	addSample(t, conn, "body_mass", "2024-12-04 07:00 -0500", 78.5, iphone) // same moment: iPhone beats manual

	s, err := LatestSample(context.Background(), conn, "weight")
	if err != nil {
		t.Fatal(err)
	}
	if s.Value != 78.5 || s.Source != iphone || s.Day != "2024-12-04" || s.Start.Hour() != 7 || s.Metric != "body_mass" {
		t.Errorf("got %+v", s)
	}
}
