package render

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sampleArt is a synthetic cover: four coloured quadrants.
func sampleArt() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 300, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 300; x++ {
			c := color.RGBA{0x40, 0x60, 0xa0, 0xff}
			if x >= 150 {
				c = color.RGBA{0xa0, 0x40, 0x60, 0xff}
			}
			if y >= 150 {
				c.G += 0x40
			}
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestKeyVariants(t *testing.T) {
	info := Info{Title: "Lifestyles of the Rich & Famous", Artist: "Good Charlotte", Position: 65 * time.Second, Duration: 190 * time.Second, Playing: true, Art: sampleArt()}
	variants := map[string]struct {
		info Info
		opts Options
	}{
		"full-playing":   {info, DefaultOptions},
		"full-paused":    {func() Info { i := info; i.Playing = false; return i }(), DefaultOptions},
		"remote":         {func() Info { i := info; i.Remote = true; return i }(), DefaultOptions},
		"no-art":         {info, Options{ShowTitle: true, ShowArtist: true, ShowTime: true, ShowProgress: true, TextScale: 1}},
		"art-only":       {info, Options{ShowArt: true}},
		"big-text":       {info, Options{ShowArt: true, ShowTitle: true, ShowArtist: true, TextScale: 1.3}},
		"unavailable":    {Info{Unavailable: true}, DefaultOptions},
		"very-long-name": {func() Info { i := info; i.Title = strings.Repeat("Supercalifragilistic ", 3); return i }(), DefaultOptions},
	}
	out := filepath.Join(os.TempDir(), "opendeck-spotify-render")
	os.MkdirAll(out, 0o755)
	for name, v := range variants {
		img := Key(v.info, v.opts)
		if img.Bounds().Dx() != Size || img.Bounds().Dy() != Size {
			t.Errorf("%s: size %v", name, img.Bounds())
		}
		f, _ := os.Create(filepath.Join(out, name+".png"))
		png.Encode(f, img)
		f.Close()
	}
	t.Logf("variants written to %s", out)

	// The progress bar is green up to 65/190 of its width and grey after.
	img := Key(info, DefaultOptions)
	if c := img.RGBAAt(20, Size-8); c != accent {
		t.Errorf("progress bar start = %v, want accent", c)
	}
	if c := img.RGBAAt(Size-12, Size-8); c != trackColor {
		t.Errorf("progress bar end = %v, want track colour", c)
	}
	// Data URL shape.
	if u := DataURL(img); !strings.HasPrefix(u, "data:image/png;base64,") || len(u) < 1000 {
		t.Errorf("data url = %.40s... (%d chars)", u, len(u))
	}
}

func TestFitTruncates(t *testing.T) {
	img := Key(Info{Title: strings.Repeat("A", 200), Duration: time.Minute}, Options{ShowTitle: true})
	// Nothing to assert on pixels precisely; the call must not panic and
	// must draw within bounds. Check a pixel far right is background-ish
	// (the ellipsis stops before the edge).
	if c := img.RGBAAt(Size-2, Size-30); c.R > 0x40 {
		t.Errorf("text overflowed to the right edge: %v", c)
	}
}
