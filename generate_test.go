package effect

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Generate reports errors instead of exiting: offline without a cache every
// download fails.
func TestGenerateOfflineWithoutCache(t *testing.T) {
	err := Generate(context.Background(), Options{Source: Source{CacheDir: t.TempDir(), Offline: true}}, func(*Result) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("want an offline error, got %v", err)
	}
}

func TestItems(t *testing.T) {
	items, err := Items()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 15 || items[0] != "notgarupa-skin00" {
		t.Fatalf("items %v", items)
	}
}

// TestCueSheets checks the cue sheet reader against known original values;
// it needs the official bundles in EFFECT_TEST_CACHE (a -cache directory).
func TestCueSheets(t *testing.T) {
	cache := os.Getenv("EFFECT_TEST_CACHE")
	if cache == "" {
		t.Skip("EFFECT_TEST_CACHE not set")
	}
	bundles := Source{CacheDir: cache, Offline: true}.cdn(context.Background())
	for _, want := range []struct {
		skin             string
		rate, start, end int
	}{{"skin00", 44100, 0, 22997}, {"skin01", 48000, 0, 25031}, {"skin02", 44100, 0, 22997}, {"skin03", 44100, 0, 22997}} {
		cues, err := cueSheet(bundles, "sound/tapseskin/"+want.skin, tapSECueSheet)
		if err != nil {
			t.Fatal(err)
		}
		hold := cues[holdCue]
		if hold == nil || hold.Loop == nil || hold.SampleRate != want.rate || hold.Loop[0] != want.start || hold.Loop[1] != want.end {
			t.Errorf("%s hold %+v", want.skin, hold)
		}
		for name, c := range cues {
			if c.Limit != 4 {
				t.Errorf("%s %s limit %d", want.skin, name, c.Limit)
			}
		}
	}
}
