package tray

import (
	"fmt"
	"math"
	"strings"

	"github.com/tlmanz/PowerPilot/internal/monitor"
)

// view is what the tray shows for one reading.
type view struct {
	glyph   glyph
	title   string    // beside the icon (macOS, Linux): the charge, when it matters
	tooltip string    // on hover (macOS, Windows)
	rows    [4]string // the menu's reading: state, battery, load, power
	mute    bool      // an alarm is sounding: offer to mute it
}

// connecting is the view until the service first answers.
var connecting = view{glyph: glyph{level: -1}, tooltip: "PowerPilot", rows: [4]string{"Connecting to PowerPilot…"}}

// viewOf shows st, or that the service can't be reached when ok is false.
func viewOf(st monitor.State, ok bool) view {
	s := st.Snapshot
	v := view{glyph: glyphOf(s, ok)}
	switch {
	case !ok:
		v.rows[0] = "PowerPilot service not running"
		v.tooltip = "PowerPilot: service not running"
		return v
	case !s.Connected || s.Status == nil:
		v.rows = [4]string{"UPS not connected", clip(s.Error, 60)}
		v.tooltip = "PowerPilot: UPS not connected"
		return v
	}
	v.rows = [4]string{s.Headline(), s.BatteryLine(), s.LoadLine(), s.PowerLine()}
	v.tooltip = clip("PowerPilot: "+s.Headline(), 120)
	if s.Charge != nil {
		v.tooltip = clip(fmt.Sprintf("PowerPilot: %s · Battery %.1f%%", s.Headline(), *s.Charge), 120)
		if v.glyph.tone != good {
			v.title = fmt.Sprintf("%.1f%%", *s.Charge)
		}
	}
	v.mute = s.AlarmActive && !s.AlarmMuted && s.Status.BeeperOn
	return v
}

// glyphOf picks the icon: the battery's charge, a bolt on mains power, and an
// exclamation mark for a low battery, an overload or a fault.
func glyphOf(s monitor.Snapshot, ok bool) glyph {
	st := s.Status
	if !ok || !s.Connected || st == nil {
		return glyph{level: -1, tone: off}
	}
	g := glyph{level: -1, tone: good, badge: bolt}
	if s.Charge != nil {
		g.level = int(math.Round(clamp(*s.Charge, 0, 100) / 5))
	}
	switch {
	case st.Fault || st.LoadPercent >= 100 || st.BatteryLow || s.BatteryState == "low":
		g.tone, g.badge = alert, bang
	case st.OnBattery || st.TestRunning || s.Mode == "standby":
		g.tone, g.badge = warn, noBadge
	}
	return g
}

// clip shortens s to n characters at most.
func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}
