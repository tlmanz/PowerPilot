// Package logbook keeps PowerPilot's events and readings on disk so they can
// be browsed by date and exported. Each day has an events file and a readings
// file of JSON lines, named by local date; readings are averaged per minute.
// Days older than the retention period are deleted.
package logbook

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

const (
	eventsDir   = "events"
	readingsDir = "readings"
	dateLayout  = "2006-01-02"
	timeLayout  = "2006-01-02 15:04:05"
)

// Reading is one minute of UPS samples.
type Reading struct {
	T       int64    `json:"t"` // start of the minute, Unix ms
	Mode    string   `json:"mode"`
	In      float64  `json:"in"`
	InMin   float64  `json:"in_min"`
	InMax   float64  `json:"in_max"`
	Out     float64  `json:"out"`
	Load    float64  `json:"load"`
	LoadMax float64  `json:"load_max"`
	Batt    float64  `json:"batt"`
	Freq    float64  `json:"freq"`
	Charge  *float64 `json:"charge,omitempty"`
}

// Day is one date that has logs.
type Day struct {
	Date     string `json:"date"`
	Events   int    `json:"events"`
	Readings bool   `json:"readings"`
}

type Book struct {
	dir       string
	retention func() int // days to keep, today included
	log       *slog.Logger
	queue     chan any
	prune     chan struct{}

	// Owned by Run.
	files map[string]*dayFile // by kind
	acc   minute
}

type dayFile struct {
	date string
	f    *os.File
}

type sample struct {
	monitor.Sample
	mode string
}

// Open returns a logbook kept in dir. retention reports how many days of
// logs to keep, today included.
func Open(dir string, retention func() int, log *slog.Logger) *Book {
	return &Book{
		dir:       dir,
		retention: retention,
		log:       log,
		queue:     make(chan any, 1024),
		prune:     make(chan struct{}, 1),
		files:     map[string]*dayFile{},
	}
}

// IsDate reports whether s is a date like 2026-10-15.
func IsDate(s string) bool {
	_, err := time.Parse(dateLayout, s)
	return err == nil
}

// Event queues an event for writing; it never blocks.
func (b *Book) Event(e monitor.Event) { b.enqueue(e) }

// Sample queues a sample for the current minute's reading; it never blocks.
func (b *Book) Sample(s monitor.Sample, mode string) { b.enqueue(sample{s, mode}) }

func (b *Book) enqueue(v any) {
	select {
	case b.queue <- v:
	default:
		b.log.Warn("log queue full, dropping an entry")
	}
}

// Prune deletes days past the retention period soon, e.g. after it changed.
func (b *Book) Prune() {
	select {
	case b.prune <- struct{}{}:
	default:
	}
}

// Run writes queued entries until done is closed, then saves the minute in
// progress and closes the files.
func (b *Book) Run(done <-chan struct{}) {
	b.deleteOld(time.Now())
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	for {
		select {
		case v := <-b.queue:
			b.write(v)
		case <-b.prune:
			b.deleteOld(time.Now())
		case now := <-hourly.C:
			b.deleteOld(now)
		case <-done:
			for {
				select {
				case v := <-b.queue:
					b.write(v)
				default:
					b.flushMinute()
					b.closeFiles()
					return
				}
			}
		}
	}
}

func (b *Book) write(v any) {
	switch v := v.(type) {
	case monitor.Event:
		b.append(eventsDir, v.Time, v)
	case sample:
		start := v.T - v.T%60_000
		if b.acc.n > 0 && b.acc.start != start {
			b.flushMinute()
		}
		if b.acc.n == 0 {
			b.acc.start = start
		}
		b.acc.add(v.Sample, v.mode)
	}
}

func (b *Book) flushMinute() {
	if b.acc.n == 0 {
		return
	}
	r := b.acc.reading()
	b.append(readingsDir, time.UnixMilli(r.T), r)
	b.acc = minute{}
}

// append adds v as a JSON line to the kind's file for t's local date.
func (b *Book) append(kind string, t time.Time, v any) {
	date := t.Local().Format(dateLayout)
	df := b.files[kind]
	if df == nil || df.date != date {
		if df != nil {
			df.f.Close()
			delete(b.files, kind)
		}
		dir := filepath.Join(b.dir, kind)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			b.log.Warn("cannot create the log folder", "err", err)
			return
		}
		f, err := os.OpenFile(filepath.Join(dir, date+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
		if err != nil {
			b.log.Warn("cannot open a log file", "err", err)
			return
		}
		df = &dayFile{date, f}
		b.files[kind] = df
	}
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	if _, err := df.f.Write(append(line, '\n')); err != nil {
		b.log.Warn("cannot write to the log", "err", err)
	}
}

func (b *Book) closeFiles() {
	for kind, df := range b.files {
		df.f.Close()
		delete(b.files, kind)
	}
}

// deleteOld removes the days before the retention period.
func (b *Book) deleteOld(now time.Time) {
	first := now.Local().AddDate(0, 0, 1-max(1, b.retention())).Format(dateLayout)
	b.closeFiles() // reopened on the next write
	for _, kind := range []string{eventsDir, readingsDir} {
		for _, date := range b.dates(kind) {
			if date < first {
				if err := os.Remove(b.path(kind, date)); err != nil {
					b.log.Warn("cannot delete an old log", "err", err)
				}
			}
		}
	}
}

func (b *Book) path(kind, date string) string {
	return filepath.Join(b.dir, kind, date+".jsonl")
}

// dates lists the days that have a file of this kind, oldest first.
func (b *Book) dates(kind string) []string {
	entries, _ := os.ReadDir(filepath.Join(b.dir, kind))
	var dates []string
	for _, e := range entries {
		if date, ok := strings.CutSuffix(e.Name(), ".jsonl"); ok && IsDate(date) {
			dates = append(dates, date)
		}
	}
	return dates
}

// Days lists the days that have logs, newest first, and the logs' total size
// in bytes.
func (b *Book) Days() ([]Day, int64) {
	byDate := map[string]*Day{}
	var size int64
	day := func(date string) *Day {
		if byDate[date] == nil {
			byDate[date] = &Day{Date: date}
		}
		return byDate[date]
	}
	for _, kind := range []string{eventsDir, readingsDir} {
		for _, date := range b.dates(kind) {
			if info, err := os.Stat(b.path(kind, date)); err == nil {
				size += info.Size()
			}
			if kind == readingsDir {
				day(date).Readings = true
			} else {
				evs, _ := b.Events(date)
				day(date).Events = len(evs)
			}
		}
	}
	days := make([]Day, 0, len(byDate))
	for _, d := range byDate {
		days = append(days, *d)
	}
	slices.SortFunc(days, func(a, b Day) int { return strings.Compare(b.Date, a.Date) })
	return days, size
}

// Events returns a day's events, oldest first.
func (b *Book) Events(date string) ([]monitor.Event, error) {
	return readDay[monitor.Event](b, eventsDir, date)
}

// Readings returns a day's per-minute readings, oldest first.
func (b *Book) Readings(date string) ([]Reading, error) {
	return readDay[Reading](b, readingsDir, date)
}

// Recent returns up to n of the latest events, oldest first.
func (b *Book) Recent(n int) []monitor.Event {
	var out []monitor.Event
	dates := b.dates(eventsDir)
	for i := len(dates) - 1; i >= 0 && len(out) < n; i-- {
		evs, _ := b.Events(dates[i])
		out = append(evs, out...)
	}
	return out[max(0, len(out)-n):]
}

func readDay[T any](b *Book, kind, date string) ([]T, error) {
	out := []T{}
	if !IsDate(date) {
		return out, errors.New("not a date: " + date)
	}
	f, err := os.Open(b.path(kind, date))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		var v T
		// A line cut short by a crash doesn't parse and is skipped.
		if json.Unmarshal(sc.Bytes(), &v) == nil {
			out = append(out, v)
		}
	}
	return out, sc.Err()
}

// ExportEvents writes the events from one date to another, inclusive, as CSV.
func (b *Book) ExportEvents(w io.Writer, from, to string) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "level", "kind", "message"})
	err := eachDate(from, to, func(date string) error {
		evs, err := b.Events(date)
		for _, e := range evs {
			cw.Write([]string{e.Time.Local().Format(timeLayout), e.Level, e.Kind, e.Message})
		}
		return err
	})
	cw.Flush()
	return errors.Join(err, cw.Error())
}

// ExportReadings writes the readings from one date to another, inclusive, as CSV.
func (b *Book) ExportReadings(w io.Writer, from, to string) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"time", "mode", "input_v", "input_v_min", "input_v_max", "output_v",
		"load_pct", "load_pct_max", "battery_v", "charge_pct", "frequency_hz"})
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
	err := eachDate(from, to, func(date string) error {
		rs, err := b.Readings(date)
		for _, r := range rs {
			charge := ""
			if r.Charge != nil {
				charge = f(*r.Charge)
			}
			cw.Write([]string{time.UnixMilli(r.T).Local().Format(timeLayout), r.Mode, f(r.In), f(r.InMin), f(r.InMax),
				f(r.Out), f(r.Load), f(r.LoadMax), f(r.Batt), charge, f(r.Freq)})
		}
		return err
	})
	cw.Flush()
	return errors.Join(err, cw.Error())
}

func eachDate(from, to string, fn func(date string) error) error {
	start, err := time.Parse(dateLayout, from)
	if err != nil {
		return err
	}
	end, err := time.Parse(dateLayout, to)
	if err != nil {
		return err
	}
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if err := fn(d.Format(dateLayout)); err != nil {
			return err
		}
	}
	return nil
}

// minute accumulates one minute of samples into a Reading.
type minute struct {
	start                     int64
	n                         int
	in, out, load, batt, freq float64
	inMin, inMax, loadMax     float64
	charge                    float64
	charges                   int
	mode                      string
}

// modeRank orders work modes by how much they matter in a minute's summary:
// a minute with any time on battery reads as "battery".
var modeRank = map[string]int{"line": 1, "standby": 2, "test": 3, "battery": 4, "fault": 5}

func (m *minute) add(s monitor.Sample, mode string) {
	if m.n == 0 {
		m.inMin, m.inMax, m.mode = s.In, s.In, mode
	}
	m.n++
	m.in += s.In
	m.out += s.Out
	m.load += s.Load
	m.batt += s.Batt
	m.freq += s.Freq
	m.inMin, m.inMax = min(m.inMin, s.In), max(m.inMax, s.In)
	m.loadMax = max(m.loadMax, s.Load)
	if s.Charge != nil {
		m.charge += *s.Charge
		m.charges++
	}
	if modeRank[mode] > modeRank[m.mode] {
		m.mode = mode
	}
}

func (m *minute) reading() Reading {
	n := float64(m.n)
	r := Reading{
		T: m.start, Mode: m.mode,
		In: round(m.in / n), InMin: round(m.inMin), InMax: round(m.inMax),
		Out: round(m.out / n), Load: round(m.load / n), LoadMax: round(m.loadMax),
		Batt: round(m.batt / n), Freq: round(m.freq / n),
	}
	if m.charges > 0 {
		c := round(m.charge / float64(m.charges))
		r.Charge = &c
	}
	return r
}

func round(v float64) float64 { return math.Round(v*10) / 10 }

// Minutes turns samples into per-minute readings the way the logbook does,
// for others that need them (Publish mode sends them to the server).
type Minutes struct{ acc minute }

// Add takes a sample. When it starts a new minute, it returns the finished one.
func (m *Minutes) Add(s monitor.Sample, mode string) (Reading, bool) {
	start := s.T - s.T%60_000
	var done Reading
	ok := m.acc.n > 0 && m.acc.start != start
	if ok {
		done = m.acc.reading()
		m.acc = minute{}
	}
	if m.acc.n == 0 {
		m.acc.start = start
	}
	m.acc.add(s, mode)
	return done, ok
}
