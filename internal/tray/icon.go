package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
)

// glyph is what the tray icon shows: a battery filled to the charge, with a
// bolt on mains power or an exclamation mark when something needs attention.
type glyph struct {
	level int // charge in twentieths, 0–20; -1 when unknown
	badge badge
	tone  tone
}

type badge uint8

const (
	noBadge badge = iota
	bolt
	bang
)

type tone uint8

const (
	off   tone = iota // no reading: dimmed
	good              // on mains
	warn              // on battery
	alert             // low battery, overload, fault
)

// The battery, drawn on a 32×32 grid as signed distance fields: shapes give
// the distance to their edge, negative inside, which also antialiases them.
var (
	outline = union(
		minus(roundRect(2, 8.5, 27, 23.5, 3.6), roundRect(4.2, 10.7, 24.8, 21.3, 1.6)),
		roundRect(26.5, 12.8, 29.8, 19.2, 1.2), // the terminal
	)
	well      = roundRect(5.6, 12.1, 23.4, 19.9, 1.1) // where the charge shows
	boltShape = polygon(vec{16.8, 7.5}, vec{10, 17.3}, vec{14, 17.3}, vec{12.2, 24.5}, vec{19, 14.7}, vec{15, 14.7})
	bangShape = union(roundRect(13.4, 10.2, 15.6, 18, 1.1), circle(14.5, 21, 1.35))
)

// halo is the gap kept around a badge, so it reads over the battery.
const halo = 1.4

var (
	black = color.NRGBA{0, 0, 0, 255}
	white = color.NRGBA{255, 255, 255, 255}
	gray  = color.NRGBA{0x8e, 0x8e, 0x93, 255}
	dark  = color.NRGBA{0, 0, 0, 150}
	tones = map[tone]color.NRGBA{
		good:  {0x34, 0xc7, 0x59, 255},
		warn:  {0xff, 0x9f, 0x0a, 255},
		alert: {0xff, 0x3b, 0x30, 255},
	}
)

// render draws g size pixels square. A template icon (macOS) is black with
// alpha, for the menu bar to tint; otherwise it's in color, with a gray
// outline that shows on light and dark taskbars alike.
func render(g glyph, size int, template bool) *image.NRGBA {
	var fill, mark shape
	if g.level > 0 && g.tone != off {
		right := 5.6 + 17.8*float64(min(g.level, 20))/20
		fill = func(p vec) float64 { return math.Max(well(p), p.x-right) }
	}
	switch g.badge {
	case bolt:
		mark = boltShape
	case bang:
		mark = bangShape
	}
	lines, fillColor, markColor, haloColor := gray, tones[g.tone], white, dark
	if template {
		lines, fillColor, markColor = black, black, black
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	unit := 32 / float64(size) // grid units per pixel
	cover := func(s shape, p vec) float64 { return clamp(0.5-s(p)/unit, 0, 1) }
	for y := range size {
		for x := range size {
			p := vec{(float64(x) + 0.5) * unit, (float64(y) + 0.5) * unit}
			var c pixel
			c = c.over(lines, cover(outline, p))
			if fill != nil {
				c = c.over(fillColor, cover(fill, p))
			}
			if mark != nil {
				around := cover(func(p vec) float64 { return mark(p) - halo }, p)
				if template {
					c = c.scale(1 - around)
				} else {
					c = c.over(haloColor, around)
				}
				c = c.over(markColor, cover(mark, p))
			}
			if g.tone == off {
				c = c.scale(0.5)
			}
			img.SetNRGBA(x, y, c.nrgba())
		}
	}
	return img
}

// drawing draws an icon size pixels square.
type drawing func(size int) *image.NRGBA

func (g glyph) drawing(template bool) drawing {
	return func(size int) *image.NRGBA { return render(g, size, template) }
}

// The menu's symbols (Windows, Linux), on the same 32×32 grid.
var (
	gaugeSymbol = union(
		minus(ring(16, 19, 11, 3.4), roundRect(0, 21, 32, 32, 0)),
		segment(16, 19, 22.5, 12.5, 3.4),
	)
	plugSymbol = union(
		segment(12, 4, 12, 10, 3.2), segment(20, 4, 20, 10, 3.2),
		roundRect(7.5, 10, 24.5, 21, 4.5),
		segment(16, 20, 16, 28, 3.2),
	)
	muteSymbol = union(
		polygon(vec{4, 12}, vec{9.5, 12}, vec{16.5, 5.5}, vec{16.5, 26.5}, vec{9.5, 20}, vec{4, 20}),
		segment(21, 12, 28, 20, 3), segment(28, 12, 21, 20, 3),
	)
	infoSymbol = union(
		ring(16, 16, 12, 3.2),
		segment(16, 14.5, 16, 22, 3.4), circle(16, 9.6, 2.1),
	)
	windowSymbol = union(
		minus(roundRect(3, 5, 29, 23, 3), roundRect(5.8, 10.5, 26.2, 20.4, 1.2)),
		segment(16, 23, 16, 27.5, 3), segment(10.5, 27.5, 21.5, 27.5, 3),
	)
	powerSymbol = union(
		minus(ring(16, 17.5, 10.5, 3.4), roundRect(11.5, 0, 20.5, 16, 0)),
		segment(16, 4, 16, 15, 3.4),
	)
	dotSymbol = circle(16, 16, 7.5)
)

// symbol draws s in c.
func symbol(s shape, c color.NRGBA) drawing {
	return func(size int) *image.NRGBA {
		img := image.NewNRGBA(image.Rect(0, 0, size, size))
		unit := 32 / float64(size)
		for y := range size {
			for x := range size {
				p := vec{(float64(x) + 0.5) * unit, (float64(y) + 0.5) * unit}
				img.SetNRGBA(x, y, pixel{}.over(c, clamp(0.5-s(p)/unit, 0, 1)).nrgba())
			}
		}
		return img
	}
}

// pngOf is d as a PNG.
func pngOf(d drawing, size int) []byte {
	var b bytes.Buffer
	png.Encode(&b, d(size))
	return b.Bytes()
}

// icoOf is d as a Windows icon, in the sizes Windows picks from at
// different display scales.
func icoOf(d drawing) []byte {
	sizes := []int{16, 24, 32, 48}
	images := make([][]byte, len(sizes))
	for i, s := range sizes {
		images[i] = pngOf(d, s)
	}
	// ICONDIR, one ICONDIRENTRY per image, then the images: PNG-compressed
	// entries work since Windows Vista.
	var b bytes.Buffer
	w := func(v any) { binary.Write(&b, binary.LittleEndian, v) }
	w([3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		w([4]uint8{uint8(s), uint8(s), 0, 0})
		w([2]uint16{1, 32}) // planes, bits per pixel
		w([2]uint32{uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, img := range images {
		b.Write(img)
	}
	return b.Bytes()
}

type vec struct{ x, y float64 }

// shape is a signed distance field, in grid units.
type shape func(vec) float64

func roundRect(x0, y0, x1, y1, r float64) shape {
	cx, cy := (x0+x1)/2, (y0+y1)/2
	hx, hy := (x1-x0)/2-r, (y1-y0)/2-r
	return func(p vec) float64 {
		qx, qy := math.Abs(p.x-cx)-hx, math.Abs(p.y-cy)-hy
		return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
	}
}

func circle(cx, cy, r float64) shape {
	return func(p vec) float64 { return math.Hypot(p.x-cx, p.y-cy) - r }
}

// ring is a circle's outline, w wide.
func ring(cx, cy, r, w float64) shape {
	return func(p vec) float64 { return math.Abs(math.Hypot(p.x-cx, p.y-cy)-r) - w/2 }
}

// segment is a line from a to b, w wide with round ends.
func segment(ax, ay, bx, by, w float64) shape {
	return func(p vec) float64 {
		ex, ey := bx-ax, by-ay
		wx, wy := p.x-ax, p.y-ay
		t := clamp((wx*ex+wy*ey)/(ex*ex+ey*ey), 0, 1)
		return math.Hypot(wx-ex*t, wy-ey*t) - w/2
	}
}

func polygon(pts ...vec) shape {
	return func(p vec) float64 {
		d, inside := math.Inf(1), false
		for i, a := range pts {
			b := pts[(i+1)%len(pts)]
			ex, ey := b.x-a.x, b.y-a.y
			wx, wy := p.x-a.x, p.y-a.y
			t := clamp((wx*ex+wy*ey)/(ex*ex+ey*ey), 0, 1)
			d = math.Min(d, math.Hypot(wx-ex*t, wy-ey*t))
			if (a.y > p.y) != (b.y > p.y) && p.x < a.x+(p.y-a.y)*ex/ey {
				inside = !inside
			}
		}
		if inside {
			return -d
		}
		return d
	}
}

func union(shapes ...shape) shape {
	return func(p vec) float64 {
		d := math.Inf(1)
		for _, s := range shapes {
			d = math.Min(d, s(p))
		}
		return d
	}
}

func minus(a, b shape) shape {
	return func(p vec) float64 { return math.Max(a(p), -b(p)) }
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// pixel is a color with premultiplied alpha, 0–1, for compositing.
type pixel struct{ r, g, b, a float64 }

// over paints c at coverage k on top of p.
func (p pixel) over(c color.NRGBA, k float64) pixel {
	a := float64(c.A) / 255 * k
	return pixel{
		float64(c.R)/255*a + p.r*(1-a),
		float64(c.G)/255*a + p.g*(1-a),
		float64(c.B)/255*a + p.b*(1-a),
		a + p.a*(1-a),
	}
}

func (p pixel) scale(k float64) pixel { return pixel{p.r * k, p.g * k, p.b * k, p.a * k} }

func (p pixel) nrgba() color.NRGBA {
	if p.a <= 0 {
		return color.NRGBA{}
	}
	u := func(v float64) uint8 { return uint8(math.Round(clamp(v, 0, 1) * 255)) }
	return color.NRGBA{u(p.r / p.a), u(p.g / p.a), u(p.b / p.a), u(p.a)}
}
