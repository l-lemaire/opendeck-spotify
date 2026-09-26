// Package render draws the play/pause key: album cover, title, artist,
// timing and a progress bar, according to the user's display options. It
// produces a 144x144 image (a Stream Deck key at 2x) and the base64 data
// URL OpenDeck's setImage expects.
package render

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strings"
	"time"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Size is the key image side in pixels.
const Size = 144

// Options are the user's display choices.
type Options struct {
	ShowArt      bool
	ShowTitle    bool
	ShowArtist   bool
	ShowTime     bool
	ShowProgress bool
	// TextScale multiplies the default font sizes; 1 is normal.
	TextScale float64
}

// DefaultOptions shows everything.
var DefaultOptions = Options{ShowArt: true, ShowTitle: true, ShowArtist: true, ShowTime: true, ShowProgress: true, TextScale: 1}

// Info is what to draw.
type Info struct {
	Title    string
	Artist   string
	Position time.Duration
	Duration time.Duration
	Playing  bool
	// Art may be nil when unknown or disabled.
	Art image.Image
	// Remote marks playback on another device.
	Remote bool
	// Unavailable draws the "nothing to control" key: no client, no login.
	Unavailable bool
}

var (
	background = color.RGBA{0x14, 0x14, 0x14, 0xff}
	textColor  = color.RGBA{0xf2, 0xf2, 0xf2, 0xff}
	dimColor   = color.RGBA{0xb0, 0xb0, 0xb4, 0xff}
	accent     = color.RGBA{0x1d, 0xb9, 0x54, 0xff} // Spotify green
	trackColor = color.RGBA{0x50, 0x50, 0x56, 0xff}
)

// Fonts are parsed once. gofont ships the Go typefaces with the library,
// so no file to embed and no licence to add.
var (
	regular, _ = opentype.Parse(goregular.TTF)
	bold, _    = opentype.Parse(gobold.TTF)
)

// Key draws the key image.
func Key(info Info, o Options) *image.RGBA {
	if o.TextScale <= 0 {
		o.TextScale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(img, img.Bounds(), image.NewUniform(background), image.Point{}, draw.Src)

	if info.Unavailable {
		drawCentered(img, "Spotify", bold, 20*o.TextScale, Size/2-6, dimColor)
		drawCentered(img, "not available", regular, 12*o.TextScale, Size/2+14, dimColor)
		return img
	}

	// Font sizes at scale 1, and the line heights that go with them.
	titleSize, artistSize, timeSize := 17*o.TextScale, 14*o.TextScale, 13*o.TextScale

	// Work out how tall the text block is, bottom-aligned above the bar,
	// so the shaded band can start just above it.
	y := float64(Size) - 8
	if o.ShowProgress {
		y -= 8
	}
	top := y
	showTime := o.ShowTime && info.Duration > 0
	showArtist := o.ShowArtist && info.Artist != ""
	showTitle := o.ShowTitle && info.Title != ""
	if showTime {
		top -= timeSize * 1.35
	}
	if showArtist {
		top -= artistSize * 1.35
	}
	if showTitle {
		top -= titleSize * 1.35
	}

	if o.ShowArt && info.Art != nil {
		drawCover(img, info.Art)
		// Darken the lower part so text stays readable over any cover.
		if showTime || showArtist || showTitle {
			start := int(top) - 14
			if start < 20 {
				start = 20
			}
			shade(img, start, Size, 0.75)
		}
	}

	if showTime {
		drawLeft(img, clock(info.Position)+" / "+clock(info.Duration), regular, timeSize, 8, y, dimColor)
		y -= timeSize * 1.35
	}
	if showArtist {
		drawLeft(img, info.Artist, regular, artistSize, 8, y, dimColor)
		y -= artistSize * 1.35
	}
	if showTitle {
		drawLeft(img, info.Title, bold, titleSize, 8, y, textColor)
	}

	if o.ShowProgress && info.Duration > 0 {
		frac := float64(info.Position) / float64(info.Duration)
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		bar := image.Rect(8, Size-10, Size-8, Size-6)
		draw.Draw(img, bar, image.NewUniform(trackColor), image.Point{}, draw.Src)
		fill := bar
		fill.Max.X = bar.Min.X + int(float64(bar.Dx())*frac+0.5)
		draw.Draw(img, fill, image.NewUniform(accent), image.Point{}, draw.Src)
	}

	drawStateBadge(img, info.Playing)
	if info.Remote {
		drawRemoteMark(img)
	}
	return img
}

// DataURL encodes the image as the base64 PNG data URL setImage wants.
func DataURL(img image.Image) string {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// drawCover scales the cover to fill the key (covers are square).
func drawCover(dst *image.RGBA, src image.Image) {
	b := src.Bounds()
	for y := 0; y < Size; y++ {
		sy := b.Min.Y + y*b.Dy()/Size
		for x := 0; x < Size; x++ {
			sx := b.Min.X + x*b.Dx()/Size
			dst.Set(x, y, src.At(sx, sy))
		}
	}
}

// shade darkens rows from y0 to y1 with a gradient reaching `strength`.
func shade(img *image.RGBA, y0, y1 int, strength float64) {
	for y := y0; y < y1; y++ {
		t := float64(y-y0) / float64(y1-y0)
		a := strength * (0.2 + 0.8*t)
		for x := 0; x < Size; x++ {
			c := img.RGBAAt(x, y)
			c.R = uint8(float64(c.R) * (1 - a))
			c.G = uint8(float64(c.G) * (1 - a))
			c.B = uint8(float64(c.B) * (1 - a))
			img.SetRGBA(x, y, c)
		}
	}
}

// drawStateBadge draws a small play triangle (paused: press to play) or
// pause bars (playing) in the top-right corner, on a dark disc.
func drawStateBadge(img *image.RGBA, playing bool) {
	cx, cy, r := Size-18, 18, 12
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				img.SetRGBA(cx+x, cy+y, color.RGBA{0x10, 0x10, 0x12, 0xe0})
			}
		}
	}
	if playing {
		draw.Draw(img, image.Rect(cx-5, cy-5, cx-2, cy+6), image.NewUniform(accent), image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(cx+2, cy-5, cx+5, cy+6), image.NewUniform(accent), image.Point{}, draw.Src)
		return
	}
	for dy := -6; dy <= 6; dy++ {
		w := 6 - abs(dy)
		for dx := 0; dx <= w; dx++ {
			img.SetRGBA(cx-3+dx, cy+dy, textColor)
		}
	}
}

// drawRemoteMark draws a small ring top-left: playback is on another device.
func drawRemoteMark(img *image.RGBA) {
	cx, cy := 16, 16
	for y := -7; y <= 7; y++ {
		for x := -7; x <= 7; x++ {
			d := x*x + y*y
			if d <= 49 && d >= 25 {
				img.SetRGBA(cx+x, cy+y, accent)
			}
		}
	}
	img.SetRGBA(cx, cy, accent)
}

func face(f *opentype.Font, size float64) font.Face {
	fc, _ := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	return fc
}

// drawLeft draws text at (x, baseline y), truncated with an ellipsis to fit.
func drawLeft(img *image.RGBA, text string, f *opentype.Font, size, x, y float64, col color.Color) {
	fc := face(f, size)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(col), Face: fc}
	text = fit(d, text, Size-int(x)-8)
	d.Dot = fixed.Point26_6{X: fixed.I(int(x)), Y: fixed.I(int(y))}
	d.DrawString(text)
}

func drawCentered(img *image.RGBA, text string, f *opentype.Font, size, y float64, col color.Color) {
	fc := face(f, size)
	d := &font.Drawer{Dst: img, Src: image.NewUniform(col), Face: fc}
	w := d.MeasureString(text).Ceil()
	d.Dot = fixed.Point26_6{X: fixed.I((Size - w) / 2), Y: fixed.I(int(y))}
	d.DrawString(text)
}

// fit shortens text until it fits in width pixels, adding an ellipsis.
func fit(d *font.Drawer, text string, width int) string {
	if d.MeasureString(text).Ceil() <= width {
		return text
	}
	runes := []rune(text)
	for len(runes) > 1 {
		runes = runes[:len(runes)-1]
		candidate := strings.TrimRight(string(runes), " ") + "…"
		if d.MeasureString(candidate).Ceil() <= width {
			return candidate
		}
	}
	return "…"
}

func clock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
