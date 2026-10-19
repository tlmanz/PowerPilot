//go:build !darwin

package tray

import (
	"runtime"

	"fyne.io/systray"
)

// Run shows the tray icon with its menu and returns once Quit is chosen.
// Call it from the main goroutine. (macOS draws its own: see Follow.)
func Run(opt Options) {
	if opt.Quit == nil {
		opt.Quit = systray.Quit
	}
	m := &menu{t: newTray(opt), icons: map[rowIcon][]byte{}}
	m.t.render = m.render
	systray.Run(m.ready, m.t.Stop)
}

// menu is the tray icon's menu: the reading in rows with icons, in the
// menu's own text color, then the actions. Choosing a reading opens the
// dashboard.
type menu struct {
	t                         *Tray
	rows                      [4]*systray.MenuItem
	mute, summary, open, quit *systray.MenuItem

	// Only the goroutine following the service uses these.
	shown     view
	rowsShown [4]bool
	rowIcons  [4]rowIcon
	icons     map[rowIcon][]byte
}

// rowIcon is the icon beside a reading: a dot in the state's color, the
// battery, or one of the symbols.
type rowIcon struct {
	kind    string // "dot", "battery" or a symbol's name
	tone    tone
	battery glyph
}

var symbols = map[string]shape{"gauge": gaugeSymbol, "plug": plugSymbol}

func (m *menu) ready() {
	for i := range m.rows {
		m.rows[i] = systray.AddMenuItem("", "Open the dashboard")
		m.rows[i].Hide()
	}
	systray.AddSeparator()
	m.mute = m.action("Mute Alarm", "Silence the UPS beeper until the alarm condition clears", muteSymbol)
	m.mute.Hide()
	m.summary = m.action("Show Summary", "Show the latest reading as a notification", infoSymbol)
	m.open = m.action("Open Dashboard", "", windowSymbol)
	systray.AddSeparator()
	m.quit = m.action("Quit", "Remove this icon; the service keeps watching the UPS", powerSymbol)
	if runtime.GOOS == "windows" {
		// As Windows apps do: a click opens the app, a right-click the menu.
		systray.SetOnTapped(m.t.opt.Open)
	}
	m.t.start()
	go m.clicks()
}

func (m *menu) action(title, tooltip string, s shape) *systray.MenuItem {
	item := systray.AddMenuItem(title, tooltip)
	item.SetIcon(encode(symbol(s, gray)))
	return item
}

func (m *menu) clicks() {
	t := m.t
	for {
		select {
		case <-t.ctx.Done():
			return
		case <-m.rows[0].ClickedCh:
			t.opt.Open()
		case <-m.rows[1].ClickedCh:
			t.opt.Open()
		case <-m.rows[2].ClickedCh:
			t.opt.Open()
		case <-m.rows[3].ClickedCh:
			t.opt.Open()
		case <-m.open.ClickedCh:
			t.opt.Open()
		case <-m.summary.ClickedCh:
			t.Summary()
		case <-m.mute.ClickedCh:
			go t.MuteAlarm()
		case <-m.quit.ClickedCh:
			t.opt.Quit()
		}
	}
}

// render shows v, changing only what changed: on Linux every change goes
// out to the panel over D-Bus.
func (m *menu) render(v view) {
	first := m.shown.tooltip == ""
	if first || v.glyph != m.shown.glyph {
		icon := m.t.icon(v.glyph)
		systray.SetTemplateIcon(icon, icon)
	}
	if v.title != m.shown.title {
		systray.SetTitle(v.title)
	}
	if v.tooltip != m.shown.tooltip {
		systray.SetTooltip(v.tooltip)
	}
	icons := [4]rowIcon{{kind: "dot", tone: v.glyph.tone}, {kind: "battery", battery: v.glyph}, {kind: "gauge"}, {kind: "plug"}}
	if v.glyph.tone == off {
		icons[1] = rowIcon{kind: "plug"} // beside why it isn't connected
	}
	for i, row := range v.rows {
		item := m.rows[i]
		if row != "" && row != m.shown.rows[i] {
			item.SetTitle(row)
		}
		if row != "" && icons[i] != m.rowIcons[i] {
			item.SetIcon(m.iconFor(icons[i]))
			m.rowIcons[i] = icons[i]
		}
		switch {
		case row != "" && !m.rowsShown[i]:
			item.Show()
		case row == "" && m.rowsShown[i]:
			item.Hide()
		}
		m.rowsShown[i] = row != ""
	}
	if first || v.mute != m.shown.mute {
		if v.mute {
			m.mute.Show()
		} else {
			m.mute.Hide()
		}
	}
	m.shown = v
}

// iconFor draws a row's icon once and keeps it.
func (m *menu) iconFor(key rowIcon) []byte {
	if icon := m.icons[key]; icon != nil {
		return icon
	}
	var d drawing
	switch key.kind {
	case "battery":
		d = key.battery.drawing(false)
	case "dot":
		c, ok := tones[key.tone]
		if !ok {
			c = gray // no reading
		}
		d = symbol(dotSymbol, c)
	default:
		d = symbol(symbols[key.kind], gray)
	}
	m.icons[key] = encode(d)
	return m.icons[key]
}

// encode is d as a menu takes it: an .ico on Windows, a small PNG on Linux,
// where menus show icons at the size they come in.
func encode(d drawing) []byte {
	if runtime.GOOS == "windows" {
		return icoOf(d)
	}
	return pngOf(d, 16)
}
