// Package effect builds the NotGarupa effect pack: the original game's
// judgement, hold, skill, clear and UI cues, named exactly as the original
// cues, plus the standard Sonolus clip names as aliases.
//
// Generate returns the effect item in memory (for embedding in a server);
// WriteSource writes it as a sonolus-pack source tree. The command in
// cmd/sonolus-notgarupa-effect-generator wraps both.
package effect

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"image/color"
	"math"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/WindowsSov8forUs/sonolus-core-go/codec"
	"github.com/WindowsSov8forUs/sonolus-core-go/core/resource"
	"github.com/WindowsSov8forUs/sonolus-core-go/database"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/audioutil"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/bestdori"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/imgutil"
	"github.com/WindowsSov8forUs/sonolus-notgarupa-effect-generator/internal/packout"
)

type cueSource struct {
	name   string // clip name = original cue name
	bundle string // Bestdori bundle ("" = APK asset under assets/apk)
	file   string
	loop   bool
	note   string
}

// Hold loop region of SE_RHYTHM_TAP_LONG in the original CRI cue (samples at 44.1 kHz).
const (
	holdLoopEnd      = 22997
	holdReleaseFadeS = 0.30000001192092896
	holdCue          = "SE_RHYTHM_TAP_LONG"
	holdLoopClip     = holdCue + "#loop"
	holdReleaseClip  = holdCue + "#release"
	decide1APKPath   = "sound/common-se/decide_1.mp3"
)

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

// Format is the clip format of the generated pack.
type Format string

const (
	// WAV is gapless-decoded PCM with the original timing (MP3 encoder delay
	// removed).
	WAV Format = "wav"
	// MP3 keeps the original Bestdori bytes.
	MP3 Format = "mp3"
)

// Source configures where downloadable assets come from.
type Source struct {
	// CacheDir caches downloads (Bestdori files under <CacheDir>/bestdori);
	// empty disables the disk cache.
	CacheDir string
	// Regions is the Bestdori server priority order (nil:
	// jp, en, tw, cn, kr). Each bundle comes from the first server that has
	// it, so server-exclusive assets are found too.
	Regions []string
	// Offline fails every download that is not already cached.
	Offline bool
	// HTTPClient is used for downloads (nil: a client with a 90 s timeout).
	HTTPClient *http.Client
	// Log receives one line per download (nil: silent).
	Log func(format string, args ...any)
}

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

// Options selects what to build. The zero value builds the default pack.
type Options struct {
	// TapSE is the tap SE skin bundle sound/tapseskin/<id> ("" = skin00).
	TapSE string
	// DirectionalSE is the directional flick SE bundle ("" =
	// directionalflickskin00, fixed in the original).
	DirectionalSE string
	// Format is the clip format ("" = WAV).
	Format Format
	// Name is the effect item name ("" = notgarupa-<TapSE>).
	Name   string
	Source Source
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

// Contract describes every clip for the engine (written as contract.json by
// the command).
type Contract struct {
	Clips  []ContractClip `json:"clips"`
	Effect string         `json:"effect"`
	Format Format         `json:"format"`
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

// WriteSource (re)creates root as a sonolus-pack source tree holding the
// given effect items.
func WriteSource(root string, results ...*Result) error {
	src, err := packout.Init(root, packout.T("NotGarupa Effects", "NotGarupa 音效", "NotGarupa 効果音"))
	if err != nil {
		return err
	}
	for _, r := range results {
		rel, err := src.ItemDir("effects", r.Item.Name, packout.Item{
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
		if err := src.WriteFiles(rel, files); err != nil {
			return err
		}
	}
	return nil
}

// Generate builds the effect item.
func Generate(ctx context.Context, o Options) (*Result, error) {
	if o.TapSE == "" {
		o.TapSE = "skin00"
	}
	if o.DirectionalSE == "" {
		o.DirectionalSE = "directionalflickskin00"
	}
	switch o.Format {
	case "":
		o.Format = WAV
	case WAV, MP3:
	default:
		return nil, fmt.Errorf("unknown format %q (wav, mp3)", o.Format)
	}
	if o.Name == "" {
		o.Name = "notgarupa-" + strings.ToLower(o.TapSE)
	}
	bd := o.Source.bestdori(ctx)

	tapseBundle := "sound/tapseskin/" + o.TapSE
	dirBundle := "sound/tapseskin/" + o.DirectionalSE
	cues := []cueSource{
		{name: "perfect", bundle: tapseBundle, file: "perfect.mp3"},
		{name: "great", bundle: tapseBundle, file: "great.mp3"},
		{name: "good", bundle: tapseBundle, file: "good.mp3"},
		{name: "flick", bundle: tapseBundle, file: "flick.mp3"},
		{name: "game_button", bundle: tapseBundle, file: "game_button.mp3", note: "empty-lane tap"},
		{name: holdCue, bundle: tapseBundle, file: holdCue + ".mp3", note: "whole cue; use #loop for PlayLooped"},
		{name: "directional_fl", bundle: dirBundle, file: "directional_fl.mp3"},
		{name: "directional_fl_2", bundle: dirBundle, file: "directional_fl_2.mp3"},
		{name: "directional_fl_3", bundle: dirBundle, file: "directional_fl_3.mp3"},
		{name: "SE_RHYTHM_TAP_SKILL", bundle: "sound/common", file: "SE_RHYTHM_TAP_SKILL.mp3"},
		{name: "SE_RHYTHM_CUTIN_SKILL", bundle: "sound/common", file: "SE_RHYTHM_CUTIN_SKILL.mp3"},
		{name: "SE_RHYTHM_CUTIN_AUDIENCE", bundle: "sound/common", file: "SE_RHYTHM_CUTIN_AUDIENCE.mp3"},
		{name: "SE_RHYTHM_FULLCOMBO", bundle: "sound/common", file: "SE_RHYTHM_FULLCOMBO.mp3"},
		{name: "SE_RHYTHM_CLEAR", bundle: "sound/common", file: "SE_RHYTHM_CLEAR.mp3"},
		{name: "SE_RHYTHM_CLEAR_VO", bundle: "sound/common", file: "SE_RHYTHM_CLEAR_VO.mp3"},
		{name: "SE_RHYTHM_GAYA", bundle: "sound/common", file: "SE_RHYTHM_GAYA.mp3", loop: true, note: "crowd loop, whole buffer"},
		{name: "decide_1", file: decide1APKPath, note: "pause/dialog button"},
	}

	files := map[string][]byte{} // zip entries
	var clips []resource.EffectDataClip
	var meta []ContractClip
	add := func(clip, file string, data []byte, seconds float64, loop bool, source, note string) {
		files[file] = data
		clips = append(clips, resource.EffectDataClip{Name: resource.EffectClipName(clip), Filename: file})
		meta = append(meta, ContractClip{Name: clip, File: file, Seconds: seconds, Loop: loop, Source: source, Note: note})
	}

	var holdPCM *audioutil.PCM
	for _, c := range cues {
		raw, src, err := fetch(bd, c)
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
		if c.name == holdCue {
			holdPCM = pcm
		}
		seconds := float64(pcm.Frames()) / float64(pcm.SampleRate)
		if o.Format == WAV {
			add(c.name, c.name+".wav", pcm.WAV(), seconds, c.loop, src, c.note)
		} else {
			add(c.name, c.name+".mp3", raw, seconds, c.loop, src, c.note)
		}
	}

	// The original "miss"/"bad" cues are exact silence and never played; #MISS
	// still has to exist, so it points at a short silent clip.
	silence := &audioutil.PCM{SampleRate: 44100, Channels: 1, Samples: make([]float64, 4410)}
	add("miss", "miss.wav", silence.WAV(), 0.1, false, "generated silence (original miss/bad cues are silent)", "only for #MISS")

	// Hold loop: exactly the original loop region, PCM so it loops without gaps.
	if holdPCM.SampleRate != 44100 || holdPCM.Frames() < holdLoopEnd {
		return nil, fmt.Errorf("%s: unexpected format %d Hz, %d frames", holdCue, holdPCM.SampleRate, holdPCM.Frames())
	}
	loop := holdPCM.Slice(0, holdLoopEnd)
	add(holdLoopClip, holdCue+"_loop.wav", loop.WAV(), float64(holdLoopEnd)/44100, true, "derived: "+holdCue+" samples [0, 22997)",
		"PlayLooped this clip for Long/Slide holds")
	// Release tail: the original fades the loop linearly to 0 over 0.3 s; Sonolus
	// cannot fade a looped clip, so the engine stops the loop and plays this.
	n := int(math.Round(holdReleaseFadeS * 44100))
	tail := &audioutil.PCM{SampleRate: 44100, Channels: 1, Samples: make([]float64, n)}
	for i := 0; i < n; i++ {
		tail.Samples[i] = loop.Samples[i%holdLoopEnd] * (1 - float64(i)/float64(n))
	}
	add(holdReleaseClip, holdCue+"_release.wav", tail.WAV(), holdReleaseFadeS, false, "derived: "+holdLoopClip+" with 0.3 s linear fade",
		"play when a hold loop is stopped")

	// Standard Sonolus names as aliases of the original cues.
	ext := "." + string(o.Format)
	aliases := []struct{ std, target string }{
		{"#PERFECT", "perfect"}, {"#GREAT", "great"}, {"#GOOD", "good"}, {"#MISS", "miss"},
		{"#PERFECT_ALTERNATIVE", "flick"}, {"#GREAT_ALTERNATIVE", "flick"}, {"#GOOD_ALTERNATIVE", "flick"}, {"#MISS_ALTERNATIVE", "miss"},
		{"#HOLD", holdLoopClip}, {"#HOLD_ALTERNATIVE", holdLoopClip}, {"#STAGE", "game_button"},
	}
	for _, a := range aliases {
		file := a.target + ext
		switch a.target {
		case holdLoopClip:
			file = holdCue + "_loop.wav"
		case "miss":
			file = "miss.wav"
		}
		clips = append(clips, resource.EffectDataClip{Name: resource.EffectClipName(a.std), Filename: file})
		meta = append(meta, ContractClip{Name: a.std, File: file, AliasFor: a.target})
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
	title := fmt.Sprintf("NotGarupa %s", o.TapSE)
	return &Result{
		Item: database.DatabaseEffectItem{
			Name:     o.Name,
			Version:  packout.EffectVersion,
			Title:    packout.T(title, title, title),
			Subtitle: packout.T("BanG Dream! Girls Band Party! (original cues)", "BanG Dream! 少女乐团派对！（原作音效）", "バンドリ！ ガールズバンドパーティ！（原作効果音）"),
			Author:   packout.T("sonolus-notgarupa-effect-generator", "sonolus-notgarupa-effect-generator", "sonolus-notgarupa-effect-generator"),
			Tags:     []database.DatabaseTag{},
			Description: packout.T(
				fmt.Sprintf("Tap SE: %s, directional SE: %s, format: %s.", o.TapSE, o.DirectionalSE, o.Format),
				fmt.Sprintf("打击音效：%s，方向滑键音效：%s，格式：%s。", o.TapSE, o.DirectionalSE, o.Format),
				fmt.Sprintf("タップSE：%s、方向フリックSE：%s、形式：%s。", o.TapSE, o.DirectionalSE, o.Format)),
		},
		Data:      resource.EffectData{Clips: clips},
		Audio:     audioZip,
		Thumbnail: thumb,
		Contract:  Contract{Clips: meta, Effect: o.Name, Format: o.Format},
	}, nil
}

func fetch(bd *bestdori.Client, c cueSource) ([]byte, string, error) {
	if c.bundle == "" { // built into the APK
		data, err := apkFile(c.file)
		return data, "APK " + c.file, err
	}
	data, err := bd.FileByName(c.bundle, c.file)
	return data, "bestdori " + c.bundle + "/" + c.file, err
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
