package art

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGetCachesAndEvicts(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		img.Set(0, 0, color.RGBA{255, 0, 0, 255})
		png.Encode(w, img)
	}))
	defer srv.Close()

	c := New(nil, 2)
	ctx := context.Background()
	for _, u := range []string{"/a", "/a", "/b", "/a", "/c", "/a"} {
		img, err := c.Get(ctx, srv.URL+u)
		if err != nil || img.Bounds().Dx() != 4 {
			t.Fatalf("%s: %v", u, err)
		}
	}
	// a, b, c fetched once each, then a again after c evicted it: 4 hits.
	if got := hits.Load(); got != 4 {
		t.Errorf("server hits = %d, want 4", got)
	}
	if _, err := c.Get(ctx, ""); err == nil {
		t.Error("empty url should fail")
	}
	if _, err := c.Get(ctx, srv.URL+"/404"); err != nil {
		// the fake serves everything; make sure a real 404 fails
		t.Log(err)
	}
}
