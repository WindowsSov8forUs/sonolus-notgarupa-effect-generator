// Package effect builds the NotGarupa effect items: for every tap SE skin a
// player can end up with (the normal SE settings and the special skins'
// sound effect components, see internal/master), the original game's
// judgement, hold, skill and UI cues, named exactly as the original
// cues, plus the standard Sonolus clip names as aliases. The directional
// flick SE is fixed to directionalflickskin00, as in the original.
//
// Hold loop points and per-cue concurrency limits come from the original cue
// sheet (TapSE.acb in the official sound/tapseskin/<id> asset bundle); the
// audio comes from Bestdori's MP3 rips, decoded gaplessly.
//
// Generate builds every item in memory and hands each to a callback (for
// embedding in a server); SourceTree writes them as a sonolus-pack source
// tree. The command in cmd/sonolus-notgarupa-effect-generator wraps both.
package effect

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/WindowsSov8forUs/sonolus-core-go/codec"
	"github.com/WindowsSov8forUs/sonolus-core-go/core/resource"
	"github.com/WindowsSov8forUs/sonolus-core-go/database"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/audioutil"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/bestdori"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/cdn"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/cri"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/imgutil"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/master"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/packout"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/unity"
)

type cueSource struct {
	name   string // clip name = original cue name
	bundle string // Bestdori bundle ("" = APK asset under assets/apk)
	file   string
	loop   bool
	note   string
}

const (
	// holdReleaseFadeS is the original's linear fade-out of a released hold
	// loop (a code constant, not cue data).
	holdReleaseFadeS = 0.30000001192092896
	holdCue          = "SE_RHYTHM_TAP_LONG"
	holdIntroClip    = holdCue + "#intro"
	holdLoopClip     = holdCue + "#loop"
	holdReleaseClip  = holdCue + "#release"
	decide1APKPath   = "sound/common-se/decide_1.mp3"
	tapSECueSheet    = "TapSE.acb"
	directionalSheet = "DirectionalFlickSE.acb"
)

// tapSECues are the cues of a tap SE skin (sound/tapseskin/<id>).
var tapSECues = []cueSource{
	{name: "perfect", file: "perfect.mp3"},
	{name: "great", file: "great.mp3"},
	{name: "good", file: "good.mp3"},
	{name: "flick", file: "flick.mp3"},
	{name: "game_button", file: "game_button.mp3", note: "empty-lane tap"},
	{name: holdCue, file: holdCue + ".mp3", note: "whole cue; use #loop for PlayLooped"},
}

// sharedCues are the same in every item: the fixed directional flick SE,
// the RhythmGameSE / LiveMenu cues and the APK's decide_1.
var sharedCues = []cueSource{
	{name: "directional_fl", bundle: "sound/tapseskin/" + master.DirectionalSE, file: "directional_fl.mp3"},
	{name: "directional_fl_2", bundle: "sound/tapseskin/" + master.DirectionalSE, file: "directional_fl_2.mp3"},
	{name: "directional_fl_3", bundle: "sound/tapseskin/" + master.DirectionalSE, file: "directional_fl_3.mp3"},
	{name: "SE_RHYTHM_TAP_SKILL", bundle: "sound/common", file: "SE_RHYTHM_TAP_SKILL.mp3"},
	{name: "SE_RHYTHM_CUTIN_SKILL", bundle: "sound/common", file: "SE_RHYTHM_CUTIN_SKILL.mp3"},
	{name: "SE_RHYTHM_CUTIN_AUDIENCE", bundle: "sound/common", file: "SE_RHYTHM_CUTIN_AUDIENCE.mp3"},
	{name: "SE_RHYTHM_GAYA", bundle: "sound/common", file: "SE_RHYTHM_GAYA.mp3", loop: true, note: "crowd loop, whole buffer"},
	{name: "decide_1", file: decide1APKPath, note: "pause/dialog button"},
}

// ContractClip is one clip of the contract: an original cue, a derived clip
// or a standard-name alias.
type ContractClip struct {
	Name     string  `json:"name"`
	File     string  `json:"file"`
	Seconds  float64 `json:"seconds"`
	Loop     bool    `json:"loop"`
	Source   string  `json:"source"`
	AliasFor string  `json:"aliasFor,omitempty"`
	Note     string  `json:"note,omitempty"`
}

// ContractHold is the hold sound (SE_RHYTHM_TAP_LONG) of the item's cue
// sheet: the original plays the waveform from its start and, when it loops,
// repeats [LoopStart, LoopEnd); on release it fades out linearly over
// ReleaseSeconds.
type ContractHold struct {
	SampleRate int `json:"sampleRate"`
	Samples    int `json:"samples"`
	// Loop is false when the waveform does not loop (it plays once).
	Loop      bool `json:"loop"`
	LoopStart int  `json:"loopStart,omitempty"`
	LoopEnd   int  `json:"loopEnd,omitempty"`
	// IntroSeconds is LoopStart in seconds: the #intro clip plays first and
	// the #loop clip starts this much later (0: no #intro clip).
	IntroSeconds   float64 `json:"introSeconds"`
	ReleaseSeconds float64 `json:"releaseSeconds"`
}

// Contract describes every clip for the engine (written as
// contracts/<item>.json by the command).
type Contract struct {
	Effect string `json:"effect"`
	TapSE  string `json:"tapSE"`
	// DirectionalSE is the fixed directional flick SE skin.
	DirectionalSE string         `json:"directionalSE"`
	Clips         []ContractClip `json:"clips"`
	Hold          ContractHold   `json:"hold"`
	// CueLimits is the per-cue concurrency limit of the original cue sheets
	// (tap SE and directional SE cues); -1 means the cue sets no limit.
	CueLimits map[string]int `json:"cueLimits"`
}

// Source configures where downloadable assets come from.
type Source struct {
	// CacheDir caches downloads (Bestdori files under <CacheDir>/bestdori,
	// official asset bundles under <CacheDir>/cdn); empty disables the disk
	// cache.
	CacheDir string
	// Regions is the Bestdori server priority order (nil:
	// jp, en, tw, cn, kr). Each bundle comes from the first server that has
	// it, so server-exclusive assets are found too.
	Regions []string
	// AssetVersion is the official content CDN asset version of the cue
	// sheets ("" = PinnedAssetVersion).
	AssetVersion string
	// Offline fails every download that is not already cached.
	Offline bool
	// HTTPClient is used for downloads (nil: the clients' defaults).
	HTTPClient *http.Client
	// Log receives one line per download (nil: silent).
	Log func(format string, args ...any)
}

// PinnedAssetVersion is the content CDN asset version the generator was
// verified against.
const PinnedAssetVersion = cdn.PinnedVersion

func (s Source) bestdori(ctx context.Context) *bestdori.Client {
	cache := ""
	if s.CacheDir != "" {
		cache = filepath.Join(s.CacheDir, "bestdori")
	}
	b := bestdori.New(cache)
	b.Regions = s.Regions
	if s.HTTPClient != nil {
		b.HTTP = s.HTTPClient
	}
	b.Offline, b.Log, b.Ctx = s.Offline, s.Log, ctx
	return b
}

func (s Source) cdn(ctx context.Context) *cdn.Client {
	return &cdn.Client{Version: s.AssetVersion, CacheDir: s.CacheDir, Offline: s.Offline, HTTP: s.HTTPClient, Log: s.Log, Ctx: ctx}
}

// Options configures a run; the zero value builds every item.
type Options struct {
	Source Source
	// Workers is the number of items built in parallel (0: one per CPU).
	Workers int
}

// Result is one generated effect item.
type Result struct {
	// Item is the database item; its resource fields (Thumbnail, Data,
	// Audio) are left empty for the caller to fill, e.g. with the server's
	// repository URLs of Files().
	Item      database.DatabaseEffectItem
	Data      resource.EffectData
	Audio     []byte // zip of the clip files
	Thumbnail []byte // PNG
	Contract  Contract
}

// Files returns the item's resource files by item.json field name:
// "thumbnail", "data" (gzip-compressed EffectData) and "audio".
func (r *Result) Files() (map[string][]byte, error) {
	data, err := codec.Compress(r.Data)
	if err != nil {
		return nil, fmt.Errorf("compress effect data: %w", err)
	}
	return map[string][]byte{"thumbnail": r.Thumbnail, "data": data, "audio": r.Audio}, nil
}

// SourceTree is a sonolus-pack source tree that items are added to.
type SourceTree struct{ src *packout.Source }

// CreateSourceTree (re)creates root as an empty sonolus-pack source tree.
func CreateSourceTree(root string) (*SourceTree, error) {
	src, err := packout.Init(root, packout.T("NotGarupa Effects", "NotGarupa 音效", "NotGarupa 効果音"))
	if err != nil {
		return nil, err
	}
	return &SourceTree{src}, nil
}

// Add writes one item into the tree.
func (t *SourceTree) Add(r *Result) error {
	rel, err := t.src.ItemDir("effects", r.Item.Name, packout.Item{
		Version: r.Item.Version, Title: r.Item.Title, Subtitle: r.Item.Subtitle, Author: r.Item.Author,
		Tags: r.Item.Tags, Description: r.Item.Description,
	})
	if err != nil {
		return err
	}
	files, err := r.Files()
	if err != nil {
		return err
	}
	return t.src.WriteFiles(rel, files)
}

// Items returns the name of every item Generate builds, in order.
func Items() ([]string, error) {
	m, err := master.Load()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range m.Effects() {
		out = append(out, e.Name())
	}
	return out, nil
}

// decodedCue is one decoded original cue.
type decodedCue struct {
	cueSource
	raw    []byte
	pcm    *audioutil.PCM
	source string
}

// Generate builds every effect item and calls emit for each (serially, in
// completion order). It stops at the first error.
func Generate(ctx context.Context, opts Options, emit func(*Result) error) error {
	m, err := master.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	bd := opts.Source.bestdori(ctx)
	bundles := opts.Source.cdn(ctx)

	shared := make([]*decodedCue, len(sharedCues))
	for i, c := range sharedCues {
		if shared[i], err = decodeCue(bd, c); err != nil {
			return err
		}
	}
	directional, err := cueSheet(bundles, "sound/tapseskin/"+master.DirectionalSE, directionalSheet)
	if err != nil {
		return err
	}

	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	specs := m.Effects()
	jobs := make(chan master.Effect)
	results := make(chan *Result)
	errs := make(chan error, len(specs))
	var wg sync.WaitGroup
	for range min(workers, len(specs)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for spec := range jobs {
				r, err := generate(bd, bundles, spec, shared, directional)
				if err != nil {
					errs <- fmt.Errorf("%s: %w", spec.Name(), err)
					cancel()
					continue
				}
				select {
				case results <- r:
				case <-ctx.Done():
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, s := range specs {
			select {
			case jobs <- s:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		wg.Wait()
		close(results)
	}()
	for r := range results {
		if err := emit(r); err != nil {
			cancel()
			for range results {
			}
			return err
		}
	}
	select {
	case err := <-errs:
		return err
	default:
	}
	return ctx.Err()
}

func decodeCue(bd *bestdori.Client, c cueSource) (*decodedCue, error) {
	var raw []byte
	var source string
	var err error
	if c.bundle == "" { // built into the APK
		raw, err = apkFile(c.file)
		source = "APK " + c.file
	} else {
		raw, err = bd.FileByName(c.bundle, c.file)
		source = "bestdori " + c.bundle + "/" + c.file
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	pcm, err := audioutil.DecodeMP3Gapless(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.name, err)
	}
	if c.name != "SE_RHYTHM_GAYA" && c.name != "decide_1" {
		pcm = pcm.Mono() // every TapSE/RhythmGameSE cue is mono in the original
	}
	return &decodedCue{cueSource: c, raw: raw, pcm: pcm, source: source}, nil
}

// cueSheet reads the named cue sheet (a TextAsset) of an official asset bundle.
func cueSheet(bundles *cdn.Client, bundle, name string) (map[string]*cri.Cue, error) {
	data, err := bundles.Bundle(bundle)
	if err != nil {
		return nil, err
	}
	files, err := unity.ReadBundle(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", bundle, err)
	}
	for _, b := range files {
		f, err := unity.Parse(b)
		if err != nil {
			continue // resource streams
		}
		for _, id := range f.Order {
			if f.Objects[id].ClassID != 49 { // TextAsset
				continue
			}
			o, err := f.Read(id)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", bundle, err)
			}
			if o["m_Name"] != name {
				continue
			}
			script, _ := o["m_Script"].(string)
			cues, err := cri.Cues([]byte(script))
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", bundle, name, err)
			}
			return cues, nil
		}
	}
	return nil, fmt.Errorf("%s has no %s", bundle, name)
}

// generate builds one item.
func generate(bd *bestdori.Client, bundles *cdn.Client, spec master.Effect, shared []*decodedCue, directional map[string]*cri.Cue) (*Result, error) {
	bundle := "sound/tapseskin/" + spec.SoundEffect
	sheet, err := cueSheet(bundles, bundle, tapSECueSheet)
	if err != nil {
		return nil, err
	}
	var cues []*decodedCue
	for _, c := range tapSECues {
		c.bundle = bundle
		d, err := decodeCue(bd, c)
		if err != nil {
			return nil, err
		}
		cue, ok := sheet[c.name]
		if !ok {
			return nil, fmt.Errorf("%s has no cue %s", tapSECueSheet, c.name)
		}
		if d.pcm.SampleRate != cue.SampleRate {
			return nil, fmt.Errorf("%s: MP3 rip is %d Hz, the original waveform %d Hz", c.name, d.pcm.SampleRate, cue.SampleRate)
		}
		cues = append(cues, d)
	}
	// Item clip order: the tap SE cues, then the shared cues.
	ordered := append(append([]*decodedCue(nil), cues...), shared...)

	files := map[string][]byte{} // zip entries
	var clips []resource.EffectDataClip
	var meta []ContractClip
	add := func(clip, file string, data []byte, seconds float64, loop bool, source, note string) {
		files[file] = data
		clips = append(clips, resource.EffectDataClip{Name: resource.EffectClipName(clip), Filename: file})
		meta = append(meta, ContractClip{Name: clip, File: file, Seconds: seconds, Loop: loop, Source: source, Note: note})
	}
	var holdPCM *audioutil.PCM
	for _, c := range ordered {
		if c.name == holdCue {
			holdPCM = c.pcm
		}
		seconds := float64(c.pcm.Frames()) / float64(c.pcm.SampleRate)
		add(c.name, c.name+".wav", c.pcm.WAV(), seconds, c.loop, c.source, c.note)
	}

	// The original "miss"/"bad" cues are exact silence and never played; #MISS
	// still has to exist, so it points at a short silent clip.
	silence := &audioutil.PCM{SampleRate: 44100, Channels: 1, Samples: make([]float64, 4410)}
	add("miss", "miss.wav", silence.WAV(), 0.1, false, "generated silence (original miss/bad cues are silent)", "only for #MISS")

	hold, holdFile, err := holdClips(holdPCM, sheet[holdCue], add)
	if err != nil {
		return nil, err
	}
	holdMarkers(hold, sheet[holdCue].Limit, add)

	// Standard Sonolus names as aliases of the original cues.
	aliases := []struct{ std, target string }{
		{"#PERFECT", "perfect"}, {"#GREAT", "great"}, {"#GOOD", "good"}, {"#MISS", "miss"},
		{"#PERFECT_ALTERNATIVE", "flick"}, {"#GREAT_ALTERNATIVE", "flick"}, {"#GOOD_ALTERNATIVE", "flick"}, {"#MISS_ALTERNATIVE", "miss"},
		{"#HOLD", holdLoopClip}, {"#HOLD_ALTERNATIVE", holdLoopClip}, {"#STAGE", "game_button"},
	}
	for _, a := range aliases {
		target, file := a.target, a.target+".wav"
		if target == holdLoopClip {
			file = holdFile
			if !hold.Loop {
				target = holdCue
			}
		}
		clips = append(clips, resource.EffectDataClip{Name: resource.EffectClipName(a.std), Filename: file})
		meta = append(meta, ContractClip{Name: a.std, File: file, AliasFor: target})
	}

	audioZip, err := zipFiles(files)
	if err != nil {
		return nil, err
	}
	thumb, err := imgutil.EncodePNG(imgutil.Solid(64, 64, color.NRGBA{0xFF, 0x3B, 0x72, 0xFF}))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(meta, func(i, j int) bool { return meta[i].AliasFor == "" && meta[j].AliasFor != "" })
	limits := map[string]int{}
	for name, c := range sheet {
		limits[name] = c.Limit
	}
	for name, c := range directional {
		limits[name] = c.Limit
	}
	tapse := spec.SoundEffect
	title := fmt.Sprintf("NotGarupa %s", tapse)
	return &Result{
		Item: database.DatabaseEffectItem{
			Name:     spec.Name(),
			Version:  packout.EffectVersion,
			Title:    packout.T(title, title, title),
			Subtitle: packout.T("BanG Dream! Girls Band Party! (original cues)", "BanG Dream! 少女乐团派对！（原作音效）", "バンドリ！ ガールズバンドパーティ！（原作効果音）"),
			Author:   packout.T("sonolus-notgarupa-effect-generator", "sonolus-notgarupa-effect-generator", "sonolus-notgarupa-effect-generator"),
			Tags:     []database.DatabaseTag{},
			Description: packout.T(
				fmt.Sprintf("Tap SE: %s, directional SE: %s, format: wav.", tapse, master.DirectionalSE),
				fmt.Sprintf("打击音效：%s，方向滑键音效：%s，格式：wav。", tapse, master.DirectionalSE),
				fmt.Sprintf("タップSE：%s、方向フリックSE：%s、形式：wav。", tapse, master.DirectionalSE)),
		},
		Data:      resource.EffectData{Clips: clips},
		Audio:     audioZip,
		Thumbnail: thumb,
		Contract: Contract{Effect: spec.Name(), TapSE: tapse, DirectionalSE: master.DirectionalSE,
			Clips: meta, Hold: hold, CueLimits: limits},
	}, nil
}

// holdClips adds the clips derived from the hold cue and returns the hold
// contract and the file #HOLD points at.
func holdClips(pcm *audioutil.PCM, cue *cri.Cue, add func(clip, file string, data []byte, seconds float64, loop bool, source, note string)) (ContractHold, string, error) {
	if cue == nil {
		return ContractHold{}, "", errors.New(holdCue + " missing from the cue sheet")
	}
	rate := pcm.SampleRate
	hold := ContractHold{SampleRate: rate, Samples: cue.NumSamples, ReleaseSeconds: holdReleaseFadeS}
	n := int(math.Round(holdReleaseFadeS * float64(rate)))
	tail := &audioutil.PCM{SampleRate: rate, Channels: 1, Samples: make([]float64, n)}
	if cue.Loop == nil {
		// The waveform does not loop: it plays once and a release fades it.
		for i := 0; i < n && i < pcm.Frames(); i++ {
			tail.Samples[i] = pcm.Samples[i] * (1 - float64(i)/float64(n))
		}
		add(holdReleaseClip, holdCue+"_release.wav", tail.WAV(), holdReleaseFadeS, false, "derived: "+holdCue+" with 0.3 s linear fade",
			"play when a hold sound is stopped (the hold cue does not loop)")
		return hold, holdCue + ".wav", nil
	}
	start, end := cue.Loop[0], cue.Loop[1]
	if pcm.Frames() < end || start >= end {
		return hold, "", fmt.Errorf("%s: loop [%d, %d) outside the %d decoded samples", holdCue, start, end, pcm.Frames())
	}
	hold.Loop, hold.LoopStart, hold.LoopEnd = true, start, end
	hold.IntroSeconds = float64(start) / float64(rate)
	// Loop: exactly the original loop region, PCM so it loops without gaps.
	loop := pcm.Slice(start, end)
	length := end - start
	add(holdLoopClip, holdCue+"_loop.wav", loop.WAV(), float64(length)/float64(rate), true,
		fmt.Sprintf("derived: %s samples [%d, %d)", holdCue, start, end), "PlayLooped this clip for Long/Slide holds")
	// Release tail: the original fades the loop linearly to 0 over 0.3 s; Sonolus
	// cannot fade a looped clip, so the engine stops the loop and plays this.
	for i := 0; i < n; i++ {
		tail.Samples[i] = loop.Samples[i%length] * (1 - float64(i)/float64(n))
	}
	add(holdReleaseClip, holdCue+"_release.wav", tail.WAV(), holdReleaseFadeS, false, "derived: "+holdLoopClip+" with 0.3 s linear fade",
		"play when a hold loop is stopped")
	if start > 0 {
		intro := pcm.Slice(0, start)
		add(holdIntroClip, holdCue+"_intro.wav", intro.WAV(), hold.IntroSeconds, false,
			fmt.Sprintf("derived: %s samples [0, %d)", holdCue, start), "play first; start the #loop clip introSeconds later")
	}
	return hold, holdCue + "_loop.wav", nil
}

// Hold marker clips. A Sonolus engine can read neither clip lengths nor item
// metadata, so the hold sound's per-item properties are the existence of
// 1-sample silent clips (optional in the engine):
//
//	notgarupa/hold/introUsBit<k>  bit k of the intro length (LoopStart) in µs
//	notgarupa/hold/onceUsBit<k>   bit k of the cue length in µs when it does not loop
//	notgarupa/hold/noCueLimit     the hold cue has no cue limit
const (
	holdMarkerPrefix = "notgarupa/hold/"
	holdMarkerBits   = 24 // up to 16.7 s
)

func holdMarkers(hold ContractHold, limit int, add func(clip, file string, data []byte, seconds float64, loop bool, source, note string)) {
	marker := (&audioutil.PCM{SampleRate: 44100, Channels: 1, Samples: make([]float64, 1)}).WAV()
	mark := func(name, note string) { add(holdMarkerPrefix+name, "marker.wav", marker, 0, false, "marker", note) }
	bits := func(name string, seconds float64, note string) {
		us := int(math.Round(seconds * 1e6))
		for k := range holdMarkerBits {
			if us>>k&1 == 1 {
				mark(fmt.Sprintf("%s%d", name, k), fmt.Sprintf("%s: bit %d of %d µs", note, k, us))
			}
		}
	}
	if hold.Loop && hold.IntroSeconds > 0 {
		bits("introUsBit", hold.IntroSeconds, "hold intro length")
	}
	if !hold.Loop {
		bits("onceUsBit", float64(hold.Samples)/float64(hold.SampleRate), "length of the non-looping hold cue")
	}
	if limit < 0 {
		mark("noCueLimit", "the hold cue has no cue limit")
	}
}

func zipFiles(files map[string][]byte) ([]byte, error) {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(files[n]); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
