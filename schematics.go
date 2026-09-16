// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Distributed under the GNU General Public License v3.

// Procedural line-art schematics of the ten facial gestures, shown under each
// gesture instruction. Each image has two panels, "at rest" → "gesture", with
// the target muscle highlighted (orange) and the movement direction (blue arrows).
//
// Everything is drawn with the Go standard library (supersampled scanline
// rasteriser → PNG in memory), so the experiment still needs no asset file.
//
// Frontal lower-face views follow the appearance changes of the corresponding
// FACS action units (Ekman & Friesen, 1978): G1 ≈ AU10 unilateral, G2 ≈ AU12+25,
// G3 ≈ AU34, G4 ≈ AU12/AU20 lips closed, G7 ≈ AU16+25, G8 ≈ AU15, G9 ≈ AU17.
// G5, G6 (lip pressed against the incisors) and G10 (tongue against the palate)
// are drawn as mid-sagittal sections, since they are barely visible from the
// front. Muscle locations are approximate, after standard facial anatomy.
package main

import (
	"bytes"
	"image"
	"image/png"
	"math"
	"sort"
)

type pt struct{ X, Y float64 }

type rgb struct{ R, G, B uint8 }

var (
	colLine   = rgb{215, 215, 215}
	colLip    = rgb{195, 90, 100}
	colTeeth  = rgb{240, 238, 225}
	colTongue = rgb{205, 100, 125}
	colMuscle = rgb{255, 150, 40}
	colArrow  = rgb{80, 200, 255}
)

// Image layout in drawing units: two 400×420 panels and a central arrow.
const (
	schemUnitsW = 920.0
	schemUnitsH = 440.0
	panelLeftX  = 10.0
	panelRightX = 510.0
	panelY      = 10.0
	panelW      = 400.0
	supersample = 3
)

// ── Rasteriser ───────────────────────────────────────────────────────────────

type canvas struct {
	w, h   int     // supersampled size (px)
	pix    []uint8 // RGB, opaque black background
	scale  float64 // drawing units → supersampled px
	ox, oy float64 // current panel offset (units)
}

func newCanvas(wPx, hPx int) *canvas {
	c := &canvas{w: wPx * supersample, h: hPx * supersample}
	c.pix = make([]uint8, 3*c.w*c.h)
	c.scale = float64(c.w) / schemUnitsW
	return c
}

func (c *canvas) tr(p pt) (float64, float64) {
	return (p.X + c.ox) * c.scale, (p.Y + c.oy) * c.scale
}

func (c *canvas) blend(i int, col rgb, a float64) {
	mix := func(dst uint8, src uint8) uint8 { return uint8(float64(dst)*(1-a) + float64(src)*a + 0.5) }
	c.pix[3*i] = mix(c.pix[3*i], col.R)
	c.pix[3*i+1] = mix(c.pix[3*i+1], col.G)
	c.pix[3*i+2] = mix(c.pix[3*i+2], col.B)
}

// fill paints a polygon (even-odd rule).
func (c *canvas) fill(poly []pt, col rgb, a float64) {
	n := len(poly)
	if n < 3 {
		return
	}
	xs, ys := make([]float64, n), make([]float64, n)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for i, p := range poly {
		xs[i], ys[i] = c.tr(p)
		minY, maxY = math.Min(minY, ys[i]), math.Max(maxY, ys[i])
	}
	y0, y1 := clampInt(int(minY), 0, c.h-1), clampInt(int(maxY)+1, 0, c.h-1)
	var nodes []float64
	for y := y0; y <= y1; y++ {
		yc := float64(y) + 0.5
		nodes = nodes[:0]
		for i, j := 0, n-1; i < n; j, i = i, i+1 {
			if (ys[i] < yc) != (ys[j] < yc) {
				nodes = append(nodes, xs[i]+(yc-ys[i])/(ys[j]-ys[i])*(xs[j]-xs[i]))
			}
		}
		sort.Float64s(nodes)
		for k := 0; k+1 < len(nodes); k += 2 {
			x0 := clampInt(int(math.Ceil(nodes[k]-0.5)), 0, c.w)
			x1 := clampInt(int(math.Ceil(nodes[k+1]-0.5)), 0, c.w)
			for x := x0; x < x1; x++ {
				c.blend(y*c.w+x, col, a)
			}
		}
	}
}

// stroke paints a polyline of the given width (units) with round joins/caps.
func (c *canvas) stroke(path []pt, width float64, col rgb, a float64) {
	if len(path) == 0 {
		return
	}
	hw := width * c.scale / 2
	xs, ys := make([]float64, len(path)), make([]float64, len(path))
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i, p := range path {
		xs[i], ys[i] = c.tr(p)
		minX, maxX = math.Min(minX, xs[i]), math.Max(maxX, xs[i])
		minY, maxY = math.Min(minY, ys[i]), math.Max(maxY, ys[i])
	}
	bx0, by0 := clampInt(int(minX-hw), 0, c.w-1), clampInt(int(minY-hw), 0, c.h-1)
	bx1, by1 := clampInt(int(maxX+hw)+1, 0, c.w-1), clampInt(int(maxY+hw)+1, 0, c.h-1)
	bw := bx1 - bx0 + 1
	mask := make([]bool, bw*(by1-by0+1)) // one mask per stroke: no double blending at joins
	for i := range path {
		j := i + 1
		if j == len(path) {
			if len(path) > 1 {
				break
			}
			j = i
		}
		ax, ay, dx, dy := xs[i], ys[i], xs[j]-xs[i], ys[j]-ys[i]
		l2 := dx*dx + dy*dy
		sx0 := clampInt(int(math.Min(xs[i], xs[j])-hw), bx0, bx1)
		sx1 := clampInt(int(math.Max(xs[i], xs[j])+hw)+1, bx0, bx1)
		sy0 := clampInt(int(math.Min(ys[i], ys[j])-hw), by0, by1)
		sy1 := clampInt(int(math.Max(ys[i], ys[j])+hw)+1, by0, by1)
		for y := sy0; y <= sy1; y++ {
			for x := sx0; x <= sx1; x++ {
				px, py := float64(x)+0.5-ax, float64(y)+0.5-ay
				t := 0.0
				if l2 > 0 {
					t = math.Max(0, math.Min(1, (px*dx+py*dy)/l2))
				}
				ex, ey := px-t*dx, py-t*dy
				if ex*ex+ey*ey <= hw*hw {
					mask[(y-by0)*bw+(x-bx0)] = true
				}
			}
		}
	}
	for k, on := range mask {
		if on {
			c.blend((by0+k/bw)*c.w+bx0+k%bw, col, a)
		}
	}
}

// arrow draws a shaft with a filled triangular head at `to`.
func (c *canvas) arrow(from, to pt, width float64, col rgb) {
	dx, dy := to.X-from.X, to.Y-from.Y
	l := math.Hypot(dx, dy)
	ux, uy := dx/l, dy/l
	head := width * 3.2
	base := pt{to.X - ux*head, to.Y - uy*head}
	c.stroke([]pt{from, base}, width, col, 1)
	c.fill([]pt{to, {base.X - uy*head*0.6, base.Y + ux*head*0.6}, {base.X + uy*head*0.6, base.Y - ux*head*0.6}}, col, 1)
}

// png downsamples the supersampled buffer and encodes it.
func (c *canvas) png() []byte {
	w, h := c.w/supersample, c.h/supersample
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	n := supersample * supersample
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b int
			for sy := 0; sy < supersample; sy++ {
				for sx := 0; sx < supersample; sx++ {
					i := 3 * ((y*supersample+sy)*c.w + x*supersample + sx)
					r, g, b = r+int(c.pix[i]), g+int(c.pix[i+1]), b+int(c.pix[i+2])
				}
			}
			o := img.PixOffset(x, y)
			img.Pix[o], img.Pix[o+1], img.Pix[o+2], img.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(b/n), 255
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}

// ── Geometry helpers ─────────────────────────────────────────────────────────

const curveSteps = 24

func cubic(p0, p1, p2, p3 pt) []pt {
	out := make([]pt, curveSteps+1)
	for i := range out {
		t := float64(i) / curveSteps
		u := 1 - t
		a, b, c, d := u*u*u, 3*u*u*t, 3*u*t*t, t*t*t
		out[i] = pt{a*p0.X + b*p1.X + c*p2.X + d*p3.X, a*p0.Y + b*p1.Y + c*p2.Y + d*p3.Y}
	}
	return out
}

func quad(p0, p1, p2 pt) []pt {
	return cubic(p0, pt{p0.X + 2.0/3*(p1.X-p0.X), p0.Y + 2.0/3*(p1.Y-p0.Y)},
		pt{p2.X + 2.0/3*(p1.X-p2.X), p2.Y + 2.0/3*(p1.Y-p2.Y)}, p2)
}

// join concatenates curve pieces, dropping each piece's duplicated first point.
func join(parts ...[]pt) []pt {
	var out []pt
	for i, p := range parts {
		if i > 0 && len(p) > 0 {
			p = p[1:]
		}
		out = append(out, p...)
	}
	return out
}

func rev(p []pt) []pt {
	out := make([]pt, len(p))
	for i := range p {
		out[len(p)-1-i] = p[i]
	}
	return out
}

func ellipse(cx, cy, rx, ry float64) []pt {
	out := make([]pt, 48)
	for i := range out {
		a := 2 * math.Pi * float64(i) / 48
		out[i] = pt{cx + rx*math.Cos(a), cy + ry*math.Sin(a)}
	}
	return out
}

// mirror reflects a point about the vertical midline of a frontal panel.
func mirror(p pt) pt { return pt{panelW - p.X, p.Y} }

func mirrorAll(ps []pt) []pt {
	out := make([]pt, len(ps))
	for i, p := range ps {
		out[i] = mirror(p)
	}
	return out
}

func add(p, d pt) pt { return pt{p.X + d.X, p.Y + d.Y} }

// ── Frontal lower face ───────────────────────────────────────────────────────

// faceParams describes a lower-face configuration. "Left" is the left side of
// the image.
type faceParams struct {
	cornerL, cornerR pt      // mouth-corner displacement
	raiseL, raiseR   float64 // upper-lip lift on each side (exposes upper teeth)
	drop             float64 // lower lip pulled down at the centre (exposes lower teeth)
	pushUp           float64 // lower lip pushed up and everted (mentalis)
	thin             float64 // 0..1, lips pressed thin
	puff             float64 // cheek bulge
	nasoDeep         float64 // 0..1, deeper nasolabial folds
	marionette       bool    // lines below the mouth corners (corners pulled down)
	chinDimples      bool    // "orange peel" chin (mentalis)
}

type faceGeom struct {
	L, R           pt
	upperTop, stoU []pt // upper lip: vermilion border, lower edge
	stoL, lowerBot []pt // lower lip: upper edge, vermilion border
}

// overlay draws the gesture-panel annotations: muscle highlight (under the line
// art) and movement arrows (on top).
type overlay struct {
	muscle, arrows func(c *canvas, g faceGeom)
}

func (c *canvas) drawFace(f faceParams, ov overlay) {
	L := add(pt{130, 215}, f.cornerL)
	R := add(pt{270, 215}, f.cornerR)
	const sy = 219.0
	stoU := cubic(L, pt{165, sy - f.raiseL - f.pushUp}, pt{235, sy - f.raiseR - f.pushUp}, R)
	stoL := cubic(L, pt{165, sy + f.drop - f.pushUp}, pt{235, sy + f.drop - f.pushUp}, R)
	tu := 24 * (1 - 0.7*f.thin)
	tl := 28*(1-0.7*f.thin) + 0.5*f.pushUp
	upperTop := make([]pt, len(stoU))
	lowerBot := make([]pt, len(stoL))
	for i := range stoU {
		t := float64(i) / curveSteps
		bow := math.Pow(math.Sin(math.Pi*t), 0.6) * (1 - 0.3*math.Exp(-math.Pow((t-0.5)/0.07, 2)))
		upperTop[i] = pt{stoU[i].X, stoU[i].Y - tu*bow}
		lowerBot[i] = pt{stoL[i].X, stoL[i].Y + tl*math.Pow(math.Sin(math.Pi*t), 0.7)}
	}
	g := faceGeom{L, R, upperTop, stoU, stoL, lowerBot}

	// Teeth (visible wherever the lips are apart), then lips.
	c.fill(append(append([]pt{}, stoU...), rev(stoL)...), colTeeth, 1)
	c.fill(append(append([]pt{}, upperTop...), rev(stoU)...), colLip, 0.75)
	c.fill(append(append([]pt{}, stoL...), rev(lowerBot)...), colLip, 0.75)

	if ov.muscle != nil {
		ov.muscle(c, g)
	}

	// Face outline; puffed cheeks bulge outwards.
	jaw := cubic(pt{30, 0}, pt{18 - 0.9*f.puff, 170}, pt{80 - 0.9*f.puff, 385}, pt{200, 408})
	c.stroke(jaw, 3, colLine, 1)
	c.stroke(mirrorAll(jaw), 3, colLine, 1)
	if f.puff > 0 {
		cheek := cubic(pt{95, 150}, pt{50, 170}, pt{50, 280}, pt{105, 300})
		c.stroke(cheek, 2.5, colLine, 0.55)
		c.stroke(mirrorAll(cheek), 2.5, colLine, 0.55)
	}

	// Nose.
	alaL := cubic(pt{182, 10}, pt{168, 55}, pt{140, 88 - 0.3*f.raiseL}, pt{166, 112 - 0.3*f.raiseL})
	alaR := mirrorAll(cubic(pt{182, 10}, pt{168, 55}, pt{140, 88 - 0.3*f.raiseR}, pt{166, 112 - 0.3*f.raiseR}))
	c.stroke(alaL, 3, colLine, 1)
	c.stroke(alaR, 3, colLine, 1)
	c.stroke(quad(alaL[len(alaL)-1], pt{200, 128}, alaR[len(alaR)-1]), 3, colLine, 1)
	c.stroke(ellipse(183, 110-0.3*f.raiseL, 9, 4), 2, colLine, 0.6)
	c.stroke(ellipse(217, 110-0.3*f.raiseR, 9, 4), 2, colLine, 0.6)

	// Philtrum.
	top := upperTop[curveSteps/2]
	c.stroke([]pt{{192, 126}, {190, top.Y - 6}}, 2, colLine, 0.4)
	c.stroke([]pt{{208, 126}, {210, top.Y - 6}}, 2, colLine, 0.4)

	// Nasolabial folds.
	nl := cubic(pt{150, 100 - 0.3*f.raiseL}, pt{120, 130}, pt{102, 180}, add(L, pt{-16, 10}))
	nr := mirrorAll(cubic(pt{150, 100 - 0.3*f.raiseR}, pt{120, 130}, pt{102, 180}, add(mirror(R), pt{-16, 10})))
	c.stroke(nl, 2.5, colLine, 0.35+0.6*math.Min(1, f.nasoDeep+0.03*f.raiseL))
	c.stroke(nr, 2.5, colLine, 0.35+0.6*math.Min(1, f.nasoDeep+0.03*f.raiseR))

	// Lip outlines.
	c.stroke(upperTop, 3, colLine, 1)
	c.stroke(stoU, 3, colLine, 1)
	c.stroke(stoL, 3, colLine, 1)
	c.stroke(lowerBot, 3, colLine, 1)

	if f.marionette {
		c.stroke(quad(add(L, pt{-4, 10}), add(L, pt{-18, 35}), add(L, pt{-14, 70})), 2.5, colLine, 0.7)
		c.stroke(quad(add(R, pt{4, 10}), add(R, pt{18, 35}), add(R, pt{14, 70})), 2.5, colLine, 0.7)
	}

	// Labiomental crease and chin.
	c.stroke(quad(pt{168, 308 - 1.5*f.pushUp}, pt{200, 298 - 2*f.pushUp}, pt{232, 308 - 1.5*f.pushUp}), 2.5, colLine, 0.5)
	if f.chinDimples {
		for _, d := range []pt{{176, 338}, {198, 330}, {220, 340}, {186, 356}, {208, 362}, {228, 356}, {168, 358}, {198, 346}} {
			c.fill(ellipse(d.X, d.Y, 4.5, 3.5), colLine, 0.8)
		}
	}

	if ov.arrows != nil {
		ov.arrows(c, g)
	}
}

// ── Mid-sagittal section (profile facing right) ──────────────────────────────

type sagittalParams struct {
	upperPress, lowerPress float64 // 0..1, lip pressed back against the incisors
	tongueUp               bool
}

func (c *canvas) drawSagittal(s sagittalParams, ov overlay) {
	up, lp := 18*s.upperPress, 18*s.lowerPress
	stomion := pt{287 - 0.6*math.Max(up, lp), 224}

	nose := join(cubic(pt{230, 0}, pt{255, 45}, pt{320, 80}, pt{338, 105}),
		cubic(pt{338, 105}, pt{342, 130}, pt{305, 138}, pt{285, 140}))
	upperLip := join(cubic(pt{285, 140}, pt{288, 165}, pt{302 - up, 178}, pt{300 - up, 200}),
		cubic(pt{300 - up, 200}, pt{299 - up, 212}, pt{294 - up, 220}, stomion))
	lowerLip := join(cubic(stomion, pt{300 - lp, 228}, pt{305 - lp, 250}, pt{294 - lp, 266}),
		cubic(pt{294 - lp, 266}, pt{282, 282}, pt{266, 286}, pt{268, 298}))
	chin := join(cubic(pt{268, 298}, pt{298, 318}, pt{308, 362}, pt{284, 388}),
		cubic(pt{284, 388}, pt{255, 408}, pt{200, 410}, pt{150, 418}))

	upperIncisor := []pt{{252, 166}, {266, 161}, {280, 219}, {271, 224}}
	lowerIncisor := []pt{{262, 229}, {273, 233}, {261, 296}, {248, 292}}

	// Soft tissue of the lips, between the skin and the incisors.
	c.fill(append(append([]pt{}, upperLip...), pt{275, 222}, pt{262, 160}), colLip, 0.6)
	c.fill(append(append([]pt{}, lowerLip...), pt{256, 295}, pt{270, 231}), colLip, 0.6)

	var tongue []pt
	if s.tongueUp {
		tongue = join(cubic(pt{252, 176}, pt{232, 150}, pt{180, 140}, pt{122, 146}),
			cubic(pt{122, 146}, pt{88, 150}, pt{64, 200}, pt{68, 280}),
			cubic(pt{68, 280}, pt{80, 340}, pt{150, 372}, pt{215, 358}),
			cubic(pt{215, 358}, pt{250, 322}, pt{266, 230}, pt{252, 176}))
	} else {
		tongue = join(cubic(pt{250, 248}, pt{228, 226}, pt{160, 214}, pt{110, 228}),
			cubic(pt{110, 228}, pt{78, 236}, pt{62, 262}, pt{68, 300}),
			cubic(pt{68, 300}, pt{80, 346}, pt{150, 372}, pt{215, 358}),
			cubic(pt{215, 358}, pt{245, 334}, pt{256, 285}, pt{250, 244}))
	}
	c.fill(tongue, colTongue, 0.75)

	if ov.muscle != nil {
		ov.muscle(c, faceGeom{})
	}
	c.stroke(append(tongue, tongue[0]), 2.5, colLine, 0.8)

	// Hard palate → soft palate, mandible, pharyngeal wall.
	c.stroke(join(cubic(pt{252, 166}, pt{232, 138}, pt{180, 126}, pt{120, 132}),
		cubic(pt{120, 132}, pt{88, 136}, pt{70, 160}, pt{70, 196})), 3, colLine, 1)
	c.stroke(join(cubic(pt{252, 296}, pt{252, 336}, pt{246, 372}, pt{215, 386}),
		[]pt{{215, 386}, {110, 394}}), 3, colLine, 0.7)
	c.stroke(cubic(pt{44, 110}, pt{38, 200}, pt{42, 320}, pt{58, 420}), 2.5, colLine, 0.4)

	c.fill(upperIncisor, colTeeth, 1)
	c.fill(lowerIncisor, colTeeth, 1)
	c.stroke(append(upperIncisor, upperIncisor[0]), 2, rgb{150, 150, 140}, 1)
	c.stroke(append(lowerIncisor, lowerIncisor[0]), 2, rgb{150, 150, 140}, 1)

	c.stroke(nose, 3, colLine, 1)
	c.stroke(upperLip, 3, colLine, 1)
	c.stroke(lowerLip, 3, colLine, 1)
	c.stroke(chin, 3, colLine, 1)

	if ov.arrows != nil {
		ov.arrows(c, faceGeom{})
	}
}

// ── Per-gesture schematics ───────────────────────────────────────────────────

type schematic struct {
	sagittal bool
	face     faceParams
	sag      sagittalParams
	overlay
}

const muscleAlpha = 0.7

func muscleBand(c *canvas, from, to pt, w float64) {
	c.stroke([]pt{from, to}, w, colMuscle, muscleAlpha)
}

func muscleBlob(c *canvas, poly []pt) {
	c.fill(poly, colMuscle, muscleAlpha)
}

func arrow(c *canvas, from, to pt) {
	c.arrow(from, to, 5, colArrow)
}

var schematics = [NGestures]schematic{
	// G1 levator labii superioris: one side of the upper lip lifted (AU10).
	{face: faceParams{raiseL: 30, cornerL: pt{-2, -8}},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) { muscleBand(c, pt{164, 55}, pt{160, 190}, 18) },
			arrows: func(c *canvas, g faceGeom) { arrow(c, pt{128, 205}, pt{128, 155}) },
		}},
	// G2 zygomaticus major: broad smile, corners up and out (AU12+25).
	{face: faceParams{cornerL: pt{-24, -30}, cornerR: pt{24, -30}, raiseL: 8, raiseR: 8, drop: 20, nasoDeep: 0.8},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBand(c, pt{62, 50}, add(g.L, pt{-4, -4}), 20)
				muscleBand(c, mirror(pt{62, 50}), add(g.R, pt{4, -4}), 20)
			},
			arrows: func(c *canvas, g faceGeom) {
				arrow(c, add(g.L, pt{-2, 22}), add(g.L, pt{-38, -12}))
				arrow(c, add(g.R, pt{2, 22}), add(g.R, pt{38, -12}))
			},
		}},
	// G3 buccinator: cheeks puffed, lips closed (AU34).
	{face: faceParams{puff: 36, cornerL: pt{10, 0}, cornerR: pt{-10, 0}, thin: 0.3},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBlob(c, ellipse(92, 228, 34, 16))
				muscleBlob(c, ellipse(panelW-92, 228, 34, 16))
			},
			arrows: func(c *canvas, g faceGeom) {
				arrow(c, pt{58, 175}, pt{8, 175})
				arrow(c, pt{panelW - 58, 175}, pt{panelW - 8, 175})
			},
		}},
	// G4 risorius: wide closed-lip smile, corners pulled sideways (AU12/AU20).
	{face: faceParams{cornerL: pt{-30, -8}, cornerR: pt{30, -8}, thin: 0.5, nasoDeep: 0.4},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBand(c, pt{50, 238}, g.L, 14)
				muscleBand(c, mirror(pt{50, 238}), g.R, 14)
			},
			arrows: func(c *canvas, g faceGeom) {
				arrow(c, add(g.L, pt{-4, 36}), add(g.L, pt{-48, 36}))
				arrow(c, add(g.R, pt{4, 36}), add(g.R, pt{48, 36}))
			},
		}},
	// G5 orbicularis oris superioris: upper lip pressed against the upper incisors.
	{sagittal: true, sag: sagittalParams{upperPress: 1},
		overlay: overlay{
			muscle: func(c *canvas, _ faceGeom) { muscleBlob(c, ellipse(276, 192, 13, 30)) },
			arrows: func(c *canvas, _ faceGeom) { arrow(c, pt{360, 192}, pt{300, 192}) },
		}},
	// G6 orbicularis oris inferioris: lower lip pressed against the lower incisors.
	{sagittal: true, sag: sagittalParams{lowerPress: 1},
		overlay: overlay{
			muscle: func(c *canvas, _ faceGeom) { muscleBlob(c, ellipse(276, 254, 13, 28)) },
			arrows: func(c *canvas, _ faceGeom) { arrow(c, pt{360, 250}, pt{302, 250}) },
		}},
	// G7 depressor labii inferioris: lower lip pulled down, jaw closed (AU16+25).
	{face: faceParams{drop: 26, cornerL: pt{-4, 4}, cornerR: pt{4, 4}},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBand(c, pt{176, 280}, pt{158, 362}, 18)
				muscleBand(c, pt{224, 280}, pt{242, 362}, 18)
			},
			arrows: func(c *canvas, g faceGeom) { arrow(c, pt{200, 318}, pt{200, 372}) },
		}},
	// G8 depressor anguli oris: mouth corners pulled down (AU15).
	{face: faceParams{cornerL: pt{-4, 26}, cornerR: pt{4, 26}, marionette: true},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBlob(c, []pt{add(g.L, pt{2, 6}), {112, 350}, {152, 374}})
				muscleBlob(c, []pt{add(g.R, pt{-2, 6}), mirror(pt{112, 350}), mirror(pt{152, 374})})
			},
			arrows: func(c *canvas, g faceGeom) {
				arrow(c, add(g.L, pt{-30, -16}), add(g.L, pt{-30, 30}))
				arrow(c, add(g.R, pt{30, -16}), add(g.R, pt{30, 30}))
			},
		}},
	// G9 mentalis: lower lip pushed up and everted, chin wrinkled (AU17).
	{face: faceParams{pushUp: 22, cornerL: pt{8, 14}, cornerR: pt{-8, 14}, chinDimples: true},
		overlay: overlay{
			muscle: func(c *canvas, g faceGeom) {
				muscleBlob(c, ellipse(184, 346, 17, 32))
				muscleBlob(c, ellipse(216, 346, 17, 32))
			},
			arrows: func(c *canvas, g faceGeom) {
				arrow(c, pt{262, 372}, pt{262, 318})
				arrow(c, pt{138, 372}, pt{138, 318})
			},
		}},
	// G10 genioglossus: tongue pressed up against the hard palate.
	{sagittal: true, sag: sagittalParams{tongueUp: true},
		overlay: overlay{
			muscle: func(c *canvas, _ faceGeom) {
				muscleBlob(c, []pt{{236, 350}, {118, 262}, {138, 186}, {214, 180}})
			},
			arrows: func(c *canvas, _ faceGeom) { arrow(c, pt{165, 290}, pt{165, 205}) },
		}},
}

// renderSchematic draws gesture i (0-based) as a PNG of the given pixel size
// (aspect ratio schemUnitsW:schemUnitsH).
func renderSchematic(i, wPx, hPx int) []byte {
	s := schematics[i]
	c := newCanvas(wPx, hPx)

	c.ox, c.oy = panelLeftX, panelY
	if s.sagittal {
		c.drawSagittal(sagittalParams{}, overlay{})
	} else {
		c.drawFace(faceParams{}, overlay{})
	}

	c.ox = panelRightX
	if s.sagittal {
		c.drawSagittal(s.sag, s.overlay)
	} else {
		c.drawFace(s.face, s.overlay)
	}

	c.ox, c.oy = 0, 0
	c.arrow(pt{428, schemUnitsH / 2}, pt{492, schemUnitsH / 2}, 8, rgb{150, 150, 150})
	return c.png()
}
