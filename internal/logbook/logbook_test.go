package logbook

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// run feeds entries through a Book's writer and waits for it to finish.
func run(b *Book, feed func()) {
	done, finished := make(chan struct{}), make(chan struct{})
	go func() { b.Run(done); close(finished) }()
	feed()
	close(done)
	<-finished
}

func TestReadingsAveragePerMinute(t *testing.T) {
	b := Open(t.TempDir(), func() int { return 90 }, quiet())
	base := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local)
	at := func(sec int) int64 { return base.Add(time.Duration(sec) * time.Second).UnixMilli() }
	run(b, func() {
		b.Sample(monitor.Sample{T: at(0), In: 230, Out: 230, Load: 20, Batt: 27, Freq: 50, Charge: new(100.0)}, "line")
		b.Sample(monitor.Sample{T: at(30), In: 180, Out: 229, Load: 40, Batt: 25, Freq: 50, Charge: new(90.0)}, "battery")
		b.Sample(monitor.Sample{T: at(58), In: 235, Out: 231, Load: 30, Batt: 26, Freq: 50}, "line")
		b.Sample(monitor.Sample{T: at(61), In: 232, Out: 230, Load: 25, Batt: 27, Freq: 50.1}, "line")
	})
	rs, err := b.Readings("2026-10-15")
	if err != nil || len(rs) != 2 {
		t.Fatalf("readings = %+v, %v; want 2 minutes", rs, err)
	}
	r := rs[0]
	// 30 s since the sample before, but a gap in polling counts as 5 s on battery.
	if r.T != base.UnixMilli() || r.Mode != "battery" || r.In != 215 || r.InMin != 180 || r.InMax != 235 ||
		r.Load != 30 || r.LoadMax != 40 || r.Charge == nil || *r.Charge != 95 || r.BattS != 5 {
		t.Errorf("first minute = %+v (charge %v)", r, *r.Charge)
	}
	if rs[1].Mode != "line" || rs[1].Freq != 50.1 || rs[1].Charge != nil || rs[1].BattS != 0 {
		t.Errorf("second minute = %+v", rs[1])
	}
}

func TestBatterySeconds(t *testing.T) {
	base := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local).UnixMilli()
	var m Minutes
	var got []Reading
	add := func(sec float64, mode string) {
		if r, ok := m.Add(monitor.Sample{T: base + int64(sec*1000)}, mode); ok {
			got = append(got, r)
		}
	}
	// Minute 0: 6 s on battery polled every 2 s (the line poll after it adds
	// nothing), then a poll 24 s late that counts only 5 s.
	add(0, "line")
	add(2, "battery")
	add(4, "battery")
	add(6, "test")
	add(8, "line")
	add(32, "battery")
	add(56, "line")
	// Minute 1: on battery throughout, polled every 4 s from 56 s: 63.5 s of
	// polls, capped at 60.
	for s := 60.0; s <= 116; s += 4 {
		add(s, "battery")
	}
	add(119.5, "battery")
	add(120, "line") // ends minute 1
	add(180, "line") // ends minute 2
	if len(got) != 3 {
		t.Fatalf("readings %+v", got)
	}
	if got[0].BattS != 11 || got[1].BattS != 60 || got[2].BattS != 0 {
		t.Errorf("batt_s = %d, %d, %d; want 11, 60, 0", got[0].BattS, got[1].BattS, got[2].BattS)
	}
	first, _ := json.Marshal(got[0])
	last, _ := json.Marshal(got[2])
	if !strings.Contains(string(first), `"batt_s":11`) || strings.Contains(string(last), "batt_s") {
		t.Errorf("JSON: %s, %s", first, last)
	}
}

func TestEventsByDay(t *testing.T) {
	b := Open(t.TempDir(), func() int { return 90 }, quiet())
	ev := func(day, hour int, msg string) monitor.Event {
		return monitor.Event{Time: time.Date(2026, 10, day, hour, 0, 0, 0, time.Local), Kind: "info", Level: "info", Message: msg}
	}
	run(b, func() {
		b.Event(ev(14, 23, "late"))
		b.Event(ev(15, 1, "early"))
		b.Event(ev(15, 2, "later"))
	})
	evs, err := b.Events("2026-10-15")
	if err != nil || len(evs) != 2 || evs[0].Message != "early" {
		t.Fatalf("15th = %+v, %v", evs, err)
	}
	if got := b.Recent(2); len(got) != 2 || got[0].Message != "early" || got[1].Message != "later" {
		t.Errorf("Recent(2) = %+v", got)
	}
	if got := b.Recent(10); len(got) != 3 || got[0].Message != "late" {
		t.Errorf("Recent(10) = %+v", got)
	}
	days, size := b.Days()
	if len(days) != 2 || days[0] != (Day{Date: "2026-10-15", Events: 2}) || days[1].Date != "2026-10-14" || size == 0 {
		t.Errorf("Days = %+v, %d bytes", days, size)
	}
	if _, err := b.Events("../../etc/passwd"); err == nil {
		t.Error("read a log by a path that isn't a date")
	}
}

func TestRetention(t *testing.T) {
	dir := t.TempDir()
	keep := 3
	b := Open(dir, func() int { return keep }, quiet())
	today := time.Now()
	for _, ago := range []int{0, 2, 3, 40} {
		date := today.AddDate(0, 0, -ago).Format(dateLayout)
		for _, kind := range []string{eventsDir, readingsDir} {
			os.MkdirAll(filepath.Join(dir, kind), 0o700)
			os.WriteFile(b.path(kind, date), []byte("{}\n"), 0o600)
		}
	}
	os.WriteFile(filepath.Join(dir, eventsDir, "notes.txt"), nil, 0o600)
	b.deleteOld(today)
	days, _ := b.Days()
	if len(days) != 2 || days[1].Date != today.AddDate(0, 0, -2).Format(dateLayout) {
		t.Fatalf("kept %+v; want today and 2 days ago (3 days)", days)
	}
	keep = 1
	b.deleteOld(today)
	if days, _ := b.Days(); len(days) != 1 || days[0].Date != today.Format(dateLayout) {
		t.Fatalf("kept %+v; want only today", days)
	}
	if _, err := os.Stat(filepath.Join(dir, eventsDir, "notes.txt")); err != nil {
		t.Error("deleted a file that isn't a log")
	}
}

func TestTornLineSkipped(t *testing.T) {
	dir := t.TempDir()
	b := Open(dir, func() int { return 90 }, quiet())
	os.MkdirAll(filepath.Join(dir, eventsDir), 0o700)
	data := `{"time":"2026-10-15T10:00:00+05:30","kind":"info","level":"info","message":"ok"}` + "\n" + `{"time":"2026-10-15T10:01`
	os.WriteFile(b.path(eventsDir, "2026-10-15"), []byte(data), 0o600)
	if evs, err := b.Events("2026-10-15"); err != nil || len(evs) != 1 {
		t.Fatalf("events = %+v, %v", evs, err)
	}
}

func TestExportReadings(t *testing.T) {
	b := Open(t.TempDir(), func() int { return 90 }, quiet())
	t0 := time.Date(2026, 10, 15, 9, 30, 0, 0, time.Local).UnixMilli()
	run(b, func() {
		b.Sample(monitor.Sample{T: t0, In: 230.04, Out: 229.96, Load: 12, Batt: 27.3, Freq: 50, Charge: new(98.25)}, "line")
	})
	var buf bytes.Buffer
	if err := b.ExportReadings(&buf, "2026-10-14", "2026-10-16"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 || lines[1] != "2026-10-15 09:30:00,line,230.0,230.0,230.0,230.0,12.0,12.0,27.3,98.3,50.0" {
		t.Errorf("CSV:\n%s", buf.String())
	}
}
