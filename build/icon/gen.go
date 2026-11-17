//go:build ignore

// Icon generator. Everything magpie shows is drawn from one mark: the
// magpie in a cream rounded square, provided as logo-v2.png (1024×1024)
// in this folder (issue #3). The mark is a raster, so the generator draws
// from its pixels rather than redrawing it as vectors: the tiles (app,
// win, full) take the mark's own colours exactly as the artwork is; the
// tray, its flap and the macOS 26 AppIcon layer are the mark's silhouette
// — the black body and the blue tail, the white wing, belly and eye left
// as holes — in black and alpha, as the menu bar and the tiled icon want.
//
//	go run build/icon/gen.go tray internal/gui/tray.png     # 44px black template icon (macOS menu bar)
//	go run build/icon/gen.go tray-flap internal/gui/trayflap # its frames when clicked (a size after the folder for a bigger look)
//	go run build/icon/gen.go app 64 internal/gui/icon.png   # app icon at a given size
//	go run build/icon/gen.go full 512 internal/gui/assets/icons/icon-512.png # the mark full-bleed, as the web's icons are
//	go run build/icon/gen.go favicon internal/gui/assets/icons/favicon.svg   # the page's favicon, the mark in an SVG
//	go run build/icon/gen.go win 256 build/windows/icon-256.png # the Windows tile, filling its square
//	go run build/icon/gen.go win-ico internal/gui/icon-windows.ico # it at the tray's sizes, one .ico
//	make icons                                              # regenerates all of them plus magpie.icns
package main

import (
	"bytes"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"strconv"
)

//go:embed logo-v2.png
var logoPNG []byte

// The mark's ink: the black body and the blue tail. The cream tile it
// sits on and the white wing, belly and eye are left out (the white parts
// are near-white), as the previous drawing's they were cut out of the
// black. logoN is the mark's size in its own pixels.
const logoN = 1024

// trayW is how wide, of the 44 units, the tray bird's silhouette is drawn:
// 18pt of the menu bar's 22, as the glyphs beside it are, and enough
// clearance that the flap's bob, swinging a bird this wide, still keeps the
// frames within the same 36 units.
const trayW = 34

// markR is the corner radius of the mark's rounded square, its own: the
// cream tile's corners, measured on the provided mark (180.8 of 1024).
const markR = 180.8 / logoN

// The mark's ink bbox, in its own pixels, set when the mark loads, so the
// silhouette can be placed the same every time.
var inkBB [4]int

// A loaded mark: the artwork with its rounded square's alpha, and the ink
// silhouette and the blue tail as per-pixel coverage for sampling.
type markT struct {
	raw  []uint8 // RGBA, straight: the artwork masked to its rounded square
	ink  []float64
	blue []float64
	// where the tail meets the body: the blue tail's outermost point on the
	// right, and where the feet are: the middle of the dark ink's bottom
	// rows, in the mark's pixels. The flap turns the tail about the first
	// and bobs the bird about the second.
	tailSrc, bobSrc [2]float64
}

var (
	theMark *markT
	markErr error
)

// logo returns the decoded mark, loading it once.
func logo() *markT {
	if theMark == nil && markErr == nil {
		theMark, markErr = loadMark()
	}
	if markErr != nil {
		fmt.Fprintln(os.Stderr, markErr)
		os.Exit(1)
	}
	return theMark
}

func loadMark() (*markT, error) {
	im, err := png.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return nil, err
	}
	src := image.NewNRGBA(image.Rect(0, 0, logoN, logoN))
	draw.Draw(src, src.Bounds(), im, image.Point{}, draw.Src)

	m := &markT{
		raw:  make([]uint8, logoN*logoN*4),
		ink:  make([]float64, logoN*logoN),
		blue: make([]float64, logoN*logoN),
	}
	// the rounded square's alpha, 4×4 per pixel, and the ink and tail maps
	badge := logoN * markR
	for y := 0; y < logoN; y++ {
		for x := 0; x < logoN; x++ {
			p := src.Pix[(y*logoN+x)*4:]
			r, g, b := float64(p[0]), float64(p[1]), float64(p[2])
			var cov float64
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					fx := math.Min(logoN-1e-3, float64(x)+(float64(sx)+0.5)/4)
					fy := math.Min(logoN-1e-3, float64(y)+(float64(sy)+0.5)/4)
					if roundRect(fx, fy, 0, 0, logoN, logoN, badge) {
						cov += 0.0625
					}
				}
			}
			// how far from the cream tile and from the near-white parts:
			// background where either is close, ink where both are far
			dc := math.Max(math.Abs(r-253), math.Max(math.Abs(g-248), math.Abs(b-241)))
			dw := math.Max(math.Abs(r-254), math.Max(math.Abs(g-254), math.Abs(b-254)))
			d := math.Min(dc, dw)
			ink := math.Max(0, math.Min(1, (d-12)/24))
			blu := math.Max(0, math.Min(1, (b-math.Max(r, g))/60))
			m.raw[(y*logoN+x)*4] = p[0]
			m.raw[(y*logoN+x)*4+1] = p[1]
			m.raw[(y*logoN+x)*4+2] = p[2]
			m.raw[(y*logoN+x)*4+3] = uint8(cov*255 + 0.5)
			m.ink[y*logoN+x] = ink
			m.blue[y*logoN+x] = blu
		}
	}
	// the ink bbox, from the coverage
	loX, loY, hiX, hiY := logoN, logoN, -1, -1
	for y := 0; y < logoN; y++ {
		for x := 0; x < logoN; x++ {
			if m.ink[y*logoN+x] > 0.5 {
				if x < loX {
					loX = x
				}
				if x > hiX {
					hiX = x
				}
				if y < loY {
					loY = y
				}
				if y > hiY {
					hiY = y
				}
			}
		}
	}
	inkBB = [4]int{loX, loY, hiX, hiY}
	// the tail's root and the feet, as the flap's two points
	m.tailSrc = [2]float64{float64(loX), float64(loY)}
	for y := loY; y <= hiY; y++ {
		for x := loX; x <= hiX; x++ {
			if m.blue[y*logoN+x] > 0.5 && float64(x) > m.tailSrc[0] {
				m.tailSrc = [2]float64{float64(x), float64(y)}
			}
		}
	}
	feetLo, feetHi := hiX, loX
	for y := hiY - 8; y <= hiY; y++ {
		for x := loX; x <= hiX; x++ {
			if m.ink[y*logoN+x] > 0.8 && m.blue[y*logoN+x] < 0.2 {
				feetLo = min(feetLo, x)
				feetHi = max(feetHi, x)
			}
		}
	}
	m.bobSrc = [2]float64{float64(feetLo+feetHi) / 2, float64(hiY)}
	return m, nil
}

// colorOf is the mark's colour at (u, v) in its own pixels, bilinear,
// straight.
func (m *markT) colorOf(u, v float64) (uint8, uint8, uint8, float64) {
	u = math.Max(0, math.Min(logoN-1, u))
	v = math.Max(0, math.Min(logoN-1, v))
	x0 := int(math.Min(logoN-2, math.Floor(u)))
	y0 := int(math.Min(logoN-2, math.Floor(v)))
	ux, vx := u-float64(x0), v-float64(y0)
	var r, g, b, a float64
	for i := 0; i < 4; i++ {
		xx, yy := x0, y0
		if i&1 != 0 {
			xx++
		}
		if i&2 != 0 {
			yy++
		}
		wx := 1 - ux
		if xx != x0 {
			wx = ux
		}
		wy := 1 - vx
		if yy != y0 {
			wy = vx
		}
		p := m.raw[(yy*logoN+xx)*4:]
		w := wx * wy
		r += float64(p[0]) * w
		g += float64(p[1]) * w
		b += float64(p[2]) * w
		a += float64(p[3]) * w
	}
	return uint8(r + 0.5), uint8(g + 0.5), uint8(b + 0.5), a
}

// covAt is the ink coverage at (u, v), bilinear.
func (m *markT) covAt(u, v float64) float64 {
	u = math.Max(0, math.Min(logoN-1, u))
	v = math.Max(0, math.Min(logoN-1, v))
	x0 := int(math.Min(logoN-2, math.Floor(u)))
	y0 := int(math.Min(logoN-2, math.Floor(v)))
	ux, vx := u-float64(x0), v-float64(y0)
	a := m.ink[y0*logoN+x0]
	b := m.ink[y0*logoN+x0+1]
	c := m.ink[(y0+1)*logoN+x0]
	d := m.ink[(y0+1)*logoN+x0+1]
	return (a*(1-ux)+b*ux)*(1-vx) + (c*(1-ux)+d*ux)*vx
}

// blueAt is the blue tail's coverage at (u, v), bilinear.
func (m *markT) blueAt(u, v float64) float64 {
	u = math.Max(0, math.Min(logoN-1, u))
	v = math.Max(0, math.Min(logoN-1, v))
	x0 := int(math.Min(logoN-2, math.Floor(u)))
	y0 := int(math.Min(logoN-2, math.Floor(v)))
	ux, vx := u-float64(x0), v-float64(y0)
	a := m.blue[y0*logoN+x0]
	b := m.blue[y0*logoN+x0+1]
	c := m.blue[(y0+1)*logoN+x0]
	d := m.blue[(y0+1)*logoN+x0+1]
	return (a*(1-ux)+b*ux)*(1-vx) + (c*(1-ux)+d*ux)*vx
}

// fit44 puts the ink bbox, w of the 44 units wide, into the middle of the
// 44-unit grid the tray draws on, and gives the map from grid to the
// mark's pixels: u = (g - o)/k.
func fit44(w float64) (k, ox, oy float64) {
	k = w / float64(inkBB[2]-inkBB[0])
	ox = 22 - float64(inkBB[0]+inkBB[2])/2*k
	oy = 22 - float64(inkBB[1]+inkBB[3])/2*k
	return k, ox, oy
}

// The tray bird's flap is the header logo's (internal/gui/assets/app.css,
// .logo.wag): the tail turning about where it meets the body through the
// same keyframes over the same .9s, cubic-bezier(.3, 0, .3, 1) between
// each two of them, and the whole bird bobbing about its feet. The mark's
// wing is white — a hole in the silhouette — so unlike the header's
// cartoon there is no black wing to turn; the tail and the bob stay.
const (
	flapSecs  = 0.9
	flapEvery = 0.03 // a frame every 30ms: internal/gui's flap plays them so
)

// A keyframe: at is the fraction of the animation, deg the rotation.
type key struct{ at, deg float64 }

var (
	tailKeys = []key{{0, 0}, {.18, 16}, {.36, -7}, {.54, 9}, {.72, -3}, {.86, 2}, {1, 0}}
	bobKeys  = []key{{0, 0}, {.18, -4}, {.40, 2}, {.62, -1}, {1, 0}}
)

// keyed is the rotation at t of keyframes played as CSS plays them, each
// step eased by cubic-bezier(.3, 0, .3, 1).
func keyed(ks []key, t float64) float64 {
	for i := 1; i < len(ks); i++ {
		if t <= ks[i].at {
			a, b := ks[i-1], ks[i]
			return a.deg + (b.deg-a.deg)*ease((t-a.at)/(b.at-a.at))
		}
	}
	return ks[len(ks)-1].deg
}

// ease is cubic-bezier(.3, 0, .3, 1): x found for the curve's parameter by
// bisection, and its y.
func ease(x float64) float64 {
	bez := func(u, p1, p2 float64) float64 { return 3*u*(1-u)*(1-u)*p1 + 3*u*u*(1-u)*p2 + u*u*u }
	lo, hi := 0.0, 1.0
	for range 40 {
		m := (lo + hi) / 2
		if bez(m, .3, .3) < x {
			lo = m
		} else {
			hi = m
		}
	}
	return bez((lo+hi)/2, 0, 1)
}

// turn rotates p about o by deg degrees, clockwise on screen as CSS's
// rotate() is.
func turn(p, o [2]float64, deg float64) [2]float64 {
	a := deg * math.Pi / 180
	s, c := math.Sin(a), math.Cos(a)
	dx, dy := p[0]-o[0], p[1]-o[1]
	return [2]float64{o[0] + dx*c - dy*s, o[1] + dx*s + dy*c}
}

// mono renders a black+alpha image of S×S, ss×ss samples under each pixel,
// calling f with grid coordinates for the coverage it sees there.
func mono(S, ss int, f func(gx, gy float64) float64) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, S, S))
	for y := 0; y < S; y++ {
		for x := 0; x < S; x++ {
			var hit float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					gx := float64(x) + (float64(sx)+0.5)/float64(ss)
					gy := float64(y) + (float64(sy)+0.5)/float64(ss)
					hit += f(gx, gy)
				}
			}
			a := uint8(hit / float64(ss*ss) * 255)
			img.SetNRGBA(x, y, color.NRGBA{0, 0, 0, a})
		}
	}
	return img
}

// tray draws the silhouette as a macOS template image (black + alpha).
func tray() image.Image {
	m := logo()
	k, ox, oy := fit44(trayW)
	return mono(44, 8, func(gx, gy float64) float64 {
		u, v := (gx-ox)/k, (gy-oy)/k
		return m.covAt(u, v)
	})
}

// trayFlap writes the frames the tray icon plays when it is clicked,
// flap-01.png on, one every flapEvery of the animation, the still bird at
// both ends left out.
func trayFlap(dir string, S int) error {
	m := logo()
	k, ox, oy := fit44(trayW)
	// the two points, in the 44-unit grid
	g := func(p [2]float64) [2]float64 { return [2]float64{p[0]*k + ox, p[1]*k + oy} }
	bobAt := g(m.bobSrc)
	tailAt := g(m.tailSrc)
	n := int(math.Round(flapSecs / flapEvery))
	const ss = 8
	for f := 1; f < n; f++ {
		t := float64(f) / float64(n)
		bob, tl := keyed(bobKeys, t), keyed(tailKeys, t)
		// each piece is the mark sampled through the animation inverted,
		// as CSS turns the piece's group: the bob outside, the piece's own
		// turn inside. The body piece is the ink without the blue tail;
		// the tail piece is the blue tail, turning about where it meets
		// the body.
		img := mono(S, ss, func(gx, gy float64) float64 {
			p := turn([2]float64{gx, gy}, bobAt, -bob)
			ub := [2]float64{(p[0] - ox) / k, (p[1] - oy) / k}
			u, v := ub[0], ub[1]
			body := m.covAt(u, v) * (1 - m.blueAt(u, v))
			q := turn(p, tailAt, -tl)
			u2, v2 := (q[0]-ox)/k, (q[1]-oy)/k
			tail := m.covAt(u2, v2) * m.blueAt(u2, v2)
			a := body
			if tail > a {
				a = tail
			}
			return a
		})
		out, err := os.Create(fmt.Sprintf("%s/flap-%02d.png", dir, f))
		if err != nil {
			return err
		}
		err = png.Encode(out, img)
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// glyph is the bird alone, black on clear, as big on an n-pixel canvas as
// app draws it on its tile: the layer of build/darwin/AppIcon.icon, which
// macOS 26 lays on a tile of its own, light or dark (#117).
func glyph(n int) image.Image {
	m := logo()
	// the bird fills 0.66 of the canvas: the mark's ink is 0.825 of the
	// mark, the mark 0.8 of the tile (app)
	k := float64(n) * 0.66 / float64(inkBB[2]-inkBB[0])
	ox := float64(n)/2 - float64(inkBB[0]+inkBB[2])/2*k
	oy := float64(n)/2 - float64(inkBB[1]+inkBB[3])/2*k
	return mono(n, 4, func(gx, gy float64) float64 {
		return m.covAt((gx-ox)/k, (gy-oy)/k)
	})
}

// app draws the mark on macOS's icon grid: the tile is ~80% of the
// canvas, the rest left clear as the Dock and Finder expect. The mark's
// own rounded square is the tile, corners and all.
func app(n int) image.Image {
	m := logo()
	S := float64(n)
	inset := S * 0.1
	side := S - 2*inset
	k := side / logoN
	ss := 4
	if n <= 64 {
		ss = 8
	}
	return tileAt(n, inset, side, markR*side, k, ss, m, false)
}

// win is the same tile as Windows draws an app's icon: filling its square,
// as the icons beside it in the taskbar, the tray and Start do. A pixel
// is left round it from 32px up, a 32nd of the side, so the rounded
// corners' soft edge stays inside the square; under 32px that pixel would
// be most of the margin and only blur the edge, so the tile meets it.
func win(n int) image.Image {
	m := logo()
	inset := float64(n / 32)
	side := float64(n) - 2*inset
	k := side / logoN
	ss := 4
	if n <= 64 {
		ss = 8
	}
	return tileAt(n, inset, side, markR*side, k, ss, m, false)
}

// full is the mark full-bleed and opaque, as the web's icons are: the
// whole square filled, so a launcher can mask it itself.
func full(n int) image.Image {
	m := logo()
	return tileAt(n, 0, float64(n), 0, float64(n)/logoN, 4, m, true)
}

// tileAt draws the mark, scaled by k so its top-left corner sits at
// (inset, inset), into an n-pixel canvas, its corners cut to the rounded
// square of the given radius. opaque fills the whole square (the web's
// full-bleed icons); otherwise the corners stay clear.
func tileAt(n int, inset, side, radius, k float64, ss int, m *markT, opaque bool) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var tile, rr, gg, bb, aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := float64(x) + (float64(sx)+0.5)/float64(ss)
					fy := float64(y) + (float64(sy)+0.5)/float64(ss)
					if opaque || roundRect(fx, fy, inset, inset, side, side, radius) {
						u := (fx - inset) / k
						v := (fy - inset) / k
						r, g, b, a := m.colorOf(u, v)
						w := 1 / float64(ss*ss)
						tile += w
						rr += float64(r) * w
						gg += float64(g) * w
						bb += float64(b) * w
						aa += a * w
					}
				}
			}
			if tile == 0 {
				continue
			}
			img.SetNRGBA(x, y, color.NRGBA{uint8(rr/tile + 0.5), uint8(gg/tile + 0.5), uint8(bb/tile + 0.5), uint8(tile*255 + 0.5)})
		}
	}
	return img
}

// favicon writes the page's icon as an SVG carrying the mark: the page
// links icons/favicon.svg, so the mark itself rides in it as a raster the
// browser draws.
func favicon(out string) error {
	const n = 64
	m := logo()
	inset, side := n*0.1, n*0.8
	var b bytes.Buffer
	if err := png.Encode(&b, tileAt(n, inset, side, markR*side, side/logoN, 8, m, false)); err != nil {
		return err
	}
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image width="64" height="64" href="data:image/png;base64,%s"/></svg>`, base64.StdEncoding.EncodeToString(b.Bytes()))
	return os.WriteFile(out, []byte(svg), 0o644)
}

// trayICOSizes are the sizes Windows asks a tray icon for (SM_CXSMICON):
// 16px at 100% scaling, 20, 24, 32 and 40 at 125% to 250%, and 48 and 64
// beyond. Wails picks the one it needs from the .ico, where a single PNG
// was scaled down by the system and blurred.
var trayICOSizes = []int{16, 20, 24, 32, 40, 48, 64}

// winICO writes win's tile at each size into one .ico, each image a PNG
// (Windows Vista on reads them).
func winICO(out string, sizes []int) error {
	var pngs [][]byte
	for _, n := range sizes {
		var b bytes.Buffer
		if err := png.Encode(&b, win(n)); err != nil {
			return err
		}
		pngs = append(pngs, b.Bytes())
	}
	var ico bytes.Buffer
	le := binary.LittleEndian
	ico.Write(le.AppendUint16(le.AppendUint16(le.AppendUint16(nil, 0), 1), uint16(len(sizes))))
	at := 6 + 16*len(sizes)
	for i, n := range sizes {
		d := byte(n)
		if n >= 256 {
			d = 0
		}
		e := []byte{d, d, 0, 0}
		e = le.AppendUint16(e, 1)  // planes
		e = le.AppendUint16(e, 32) // bits per pixel
		e = le.AppendUint32(e, uint32(len(pngs[i])))
		e = le.AppendUint32(e, uint32(at))
		ico.Write(e)
		at += len(pngs[i])
	}
	for _, p := range pngs {
		ico.Write(p)
	}
	return os.WriteFile(out, ico.Bytes(), 0o644)
}

// roundRect is a rounded square: true inside it.
func roundRect(px, py, x, y, w, h, r float64) bool {
	if px < x || py < y || px > x+w || py > y+h {
		return false
	}
	if r <= 0 {
		return true
	}
	cx := math.Max(x+r, math.Min(px, x+w-r))
	cy := math.Max(y+r, math.Min(py, y+h-r))
	return math.Hypot(px-cx, py-cy) <= r
}

func main() {
	var img image.Image
	switch {
	case len(os.Args) == 3 && os.Args[1] == "tray":
		img = tray()
	case len(os.Args) == 3 && os.Args[1] == "tray-flap":
		if err := trayFlap(os.Args[2], 44); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case len(os.Args) == 4 && os.Args[1] == "tray-flap":
		n, _ := strconv.Atoi(os.Args[3])
		if err := trayFlap(os.Args[2], n); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case len(os.Args) == 4 && os.Args[1] == "glyph":
		n, _ := strconv.Atoi(os.Args[2])
		img = glyph(n)
	case len(os.Args) == 4 && (os.Args[1] == "app" || os.Args[1] == "app-dark"):
		// one tile in either appearance: the mark is its own artwork, and
		// macOS 26's dark Dock comes from AppIcon.icon, not a second tile
		n, _ := strconv.Atoi(os.Args[2])
		img = app(n)
	case len(os.Args) == 4 && os.Args[1] == "full":
		n, _ := strconv.Atoi(os.Args[2])
		img = full(n)
	case len(os.Args) == 3 && os.Args[1] == "favicon":
		if err := favicon(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case len(os.Args) == 4 && os.Args[1] == "win":
		n, _ := strconv.Atoi(os.Args[2])
		img = win(n)
	case len(os.Args) == 3 && os.Args[1] == "win-ico":
		if err := winICO(os.Args[2], trayICOSizes); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	default:
		fmt.Fprintln(os.Stderr, "usage: gen.go tray <out.png> | gen.go app|app-dark|win|full <size> <out.png> | gen.go tray-flap <dir> [size] | gen.go glyph <size> <out.png> | gen.go favicon <out.svg> | gen.go win-ico <out.ico>")
		os.Exit(2)
	}
	f, err := os.Create(os.Args[len(os.Args)-1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
