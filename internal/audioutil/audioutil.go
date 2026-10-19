// Package audioutil decodes MP3 cues to PCM and writes exact-length WAV files,
// used where a clip must loop seamlessly (MP3 frames add encoder padding).
package audioutil

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/hajimehoshi/go-mp3"
)

// PCM is interleaved float samples in [-1, 1].
type PCM struct {
	SampleRate int
	Channels   int
	Samples    []float64 // len = frames * Channels
}

func (p *PCM) Frames() int { return len(p.Samples) / p.Channels }

// DecodeMP3 decodes to stereo PCM (go-mp3 always outputs 2 channels); when the
// source is mono both channels are equal and Mono() collapses them.
func DecodeMP3(data []byte) (*PCM, error) {
	d, err := mp3.NewDecoder(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("mp3: %w", err)
	}
	raw, err := io.ReadAll(d)
	if err != nil {
		return nil, fmt.Errorf("mp3: %w", err)
	}
	n := len(raw) / 2
	pcm := &PCM{SampleRate: d.SampleRate(), Channels: 2, Samples: make([]float64, n)}
	for i := 0; i < n; i++ {
		pcm.Samples[i] = float64(int16(binary.LittleEndian.Uint16(raw[2*i:]))) / 32768
	}
	return pcm, nil
}

// lameGapless reads the encoder delay and padding from a LAME/Xing "Info" tag
// in the first MP3 frame. ok is false when the file has no such tag.
func lameGapless(data []byte) (delay, padding int, ok bool) {
	limit := min(len(data), 512)
	head := data[:limit]
	xing := bytes.Index(head, []byte("Xing"))
	if xing < 0 {
		xing = bytes.Index(head, []byte("Info"))
	}
	lame := bytes.Index(head, []byte("LAME"))
	if xing < 0 || lame < 0 || lame+24 > len(data) {
		return 0, 0, false
	}
	b := data[lame+21 : lame+24]
	return int(b[0])<<4 | int(b[1])>>4, int(b[1]&0x0f)<<8 | int(b[2]), true
}

// mp3DecoderDelay is the synthesis delay of a standard Layer III decoder.
const mp3DecoderDelay = 529

// DecodeMP3Gapless decodes and removes the encoder/decoder priming and padding
// using the LAME tag, so that frame 0 is the first sample of the source audio.
// go-mp3 decodes the Xing/Info frame as 1152 samples of silence, which is
// removed as well. Files without a LAME tag are returned untouched.
func DecodeMP3Gapless(data []byte) (*PCM, error) {
	pcm, err := DecodeMP3(data)
	if err != nil {
		return nil, err
	}
	delay, padding, ok := lameGapless(data)
	if !ok {
		return pcm, nil
	}
	start := 1152 + delay + mp3DecoderDelay
	end := pcm.Frames() - max(0, padding-mp3DecoderDelay)
	if start >= end {
		return nil, fmt.Errorf("mp3: gapless trim leaves no audio (start %d end %d)", start, end)
	}
	return pcm.Slice(start, end), nil
}

// Mono averages the channels.
func (p *PCM) Mono() *PCM {
	if p.Channels == 1 {
		return p
	}
	f := p.Frames()
	out := &PCM{SampleRate: p.SampleRate, Channels: 1, Samples: make([]float64, f)}
	for i := 0; i < f; i++ {
		s := 0.0
		for c := 0; c < p.Channels; c++ {
			s += p.Samples[i*p.Channels+c]
		}
		out.Samples[i] = s / float64(p.Channels)
	}
	return out
}

// Slice keeps frames [from, to).
func (p *PCM) Slice(from, to int) *PCM {
	from = max(0, from)
	to = min(p.Frames(), to)
	return &PCM{SampleRate: p.SampleRate, Channels: p.Channels, Samples: append([]float64(nil), p.Samples[from*p.Channels:to*p.Channels]...)}
}

// Gain multiplies every frame by g(frameIndex).
func (p *PCM) Gain(g func(frame int) float64) *PCM {
	out := &PCM{SampleRate: p.SampleRate, Channels: p.Channels, Samples: make([]float64, len(p.Samples))}
	for i := range p.Samples {
		out.Samples[i] = p.Samples[i] * g(i/p.Channels)
	}
	return out
}

// Concat appends q (same format) to p.
func (p *PCM) Concat(q *PCM) *PCM {
	return &PCM{SampleRate: p.SampleRate, Channels: p.Channels, Samples: append(append([]float64(nil), p.Samples...), q.Samples...)}
}

// FindDelay estimates the decoder/encoder delay of an MP3 decode by locating
// the first sample whose magnitude exceeds `threshold`. Used to align the
// MP3 mirror with the original PCM loop points.
func (p *PCM) FindOnset(threshold float64) int {
	for i := 0; i < p.Frames(); i++ {
		for c := 0; c < p.Channels; c++ {
			if math.Abs(p.Samples[i*p.Channels+c]) > threshold {
				return i
			}
		}
	}
	return 0
}

// WAV encodes 16-bit PCM WAV.
func (p *PCM) WAV() []byte {
	var buf bytes.Buffer
	dataLen := len(p.Samples) * 2
	w := func(v any) { _ = binary.Write(&buf, binary.LittleEndian, v) }
	buf.WriteString("RIFF")
	w(uint32(36 + dataLen))
	buf.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(p.Channels))
	w(uint32(p.SampleRate))
	w(uint32(p.SampleRate * p.Channels * 2))
	w(uint16(p.Channels * 2))
	w(uint16(16))
	buf.WriteString("data")
	w(uint32(dataLen))
	for _, s := range p.Samples {
		v := math.Round(math.Max(-1, math.Min(1, s)) * 32767)
		w(int16(v))
	}
	return buf.Bytes()
}
