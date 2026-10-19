package cri

import (
	"encoding/binary"
	"fmt"
)

// Cue is what the generator needs of one cue of a cue sheet.
type Cue struct {
	Name string
	// Limit is the per-cue concurrency limit of the sequence's cue-limit
	// command (opcode 0x4F or 0x49); -1 when the cue sets none.
	Limit int
	// Waveform of the cue's single track.
	SampleRate, NumSamples int
	// Loop is the waveform loop [LoopStart, LoopEnd) in samples, when the
	// waveform loops (LoopFlag with extension data).
	Loop                *[2]int
	Channels, LoopFlags int
}

// Cues reads every cue of an ACB cue sheet, following
// Cue → Sequence (limit command, track) → Track event (note-on → synth) →
// Synth → Waveform (→ waveform extension loop).
func Cues(acb []byte) (map[string]*Cue, error) {
	header, err := ParseTable(acb)
	if err != nil {
		return nil, err
	}
	if len(header.Rows) != 1 {
		return nil, fmt.Errorf("ACB header has %d rows", len(header.Rows))
	}
	h := header.Rows[0]
	tables := map[string]*Table{}
	for _, name := range []string{"CueTable", "CueNameTable", "SequenceTable", "TrackTable", "CommandTable", "SynthTable", "WaveformTable", "WaveformExtensionDataTable"} {
		if tables[name], err = Nested(h, name); err != nil {
			return nil, fmt.Errorf("ACB %s: %w", name, err)
		}
	}
	rowAt := func(table string, i int) (map[string]any, error) {
		t := tables[table]
		if i < 0 || i >= len(t.Rows) {
			return nil, fmt.Errorf("ACB %s row %d of %d", table, i, len(t.Rows))
		}
		return t.Rows[i], nil
	}
	command := func(i int) ([][2]any, error) {
		row, err := rowAt("CommandTable", i)
		if err != nil {
			return nil, err
		}
		b, _ := row["Command"].([]byte)
		var out [][2]any
		for p := 0; p+3 <= len(b); {
			op := binary.BigEndian.Uint16(b[p:])
			n := int(b[p+2])
			if p+3+n > len(b) {
				return nil, fmt.Errorf("ACB command %d truncated", i)
			}
			out = append(out, [2]any{op, b[p+3 : p+3+n]})
			p += 3 + n
			if op == 0 {
				break
			}
		}
		return out, nil
	}
	cues := map[string]*Cue{}
	for _, nameRow := range tables["CueNameTable"].Rows {
		name, _ := nameRow["CueName"].(string)
		index, _ := Uint(nameRow, "CueIndex")
		cue, err := rowAt("CueTable", index)
		if err != nil {
			return nil, err
		}
		if t, _ := Uint(cue, "ReferenceType"); t != 3 {
			return nil, fmt.Errorf("cue %s: reference type %d (only sequences are supported)", name, t)
		}
		seqIndex, _ := Uint(cue, "ReferenceIndex")
		seq, err := rowAt("SequenceTable", seqIndex)
		if err != nil {
			return nil, err
		}
		c := &Cue{Name: name, Limit: -1}
		if ci, ok := Uint(seq, "CommandIndex"); ok && ci != 0xFFFF {
			cmds, err := command(ci)
			if err != nil {
				return nil, err
			}
			for _, cmd := range cmds {
				if op := cmd[0].(uint16); (op == 0x4F || op == 0x49) && len(cmd[1].([]byte)) >= 2 {
					c.Limit = int(binary.BigEndian.Uint16(cmd[1].([]byte)))
				}
			}
		}
		tracks, _ := seq["TrackIndex"].([]byte)
		if len(tracks) != 2 {
			return nil, fmt.Errorf("cue %s: %d tracks (one expected)", name, len(tracks)/2)
		}
		track, err := rowAt("TrackTable", int(binary.BigEndian.Uint16(tracks)))
		if err != nil {
			return nil, err
		}
		event, _ := Uint(track, "EventIndex")
		cmds, err := command(event)
		if err != nil {
			return nil, err
		}
		synthIndex := -1
		for _, cmd := range cmds {
			if p := cmd[1].([]byte); cmd[0].(uint16) == 0x07D0 && len(p) >= 4 && binary.BigEndian.Uint16(p) == 2 {
				synthIndex = int(binary.BigEndian.Uint16(p[2:]))
			}
		}
		if synthIndex < 0 {
			return nil, fmt.Errorf("cue %s: no synth note-on", name)
		}
		synth, err := rowAt("SynthTable", synthIndex)
		if err != nil {
			return nil, err
		}
		refs, _ := synth["ReferenceItems"].([]byte)
		if len(refs) != 4 || binary.BigEndian.Uint16(refs) != 1 {
			return nil, fmt.Errorf("cue %s: synth %d does not reference one waveform", name, synthIndex)
		}
		wave, err := rowAt("WaveformTable", int(binary.BigEndian.Uint16(refs[2:])))
		if err != nil {
			return nil, err
		}
		c.SampleRate, _ = Uint(wave, "SamplingRate")
		c.NumSamples, _ = Uint(wave, "NumSamples")
		c.Channels, _ = Uint(wave, "NumChannels")
		c.LoopFlags, _ = Uint(wave, "LoopFlag")
		if ext, ok := Uint(wave, "ExtensionData"); ok && ext != 0xFFFF {
			loop, err := rowAt("WaveformExtensionDataTable", ext)
			if err != nil {
				return nil, err
			}
			start, _ := Uint(loop, "LoopStart")
			end, _ := Uint(loop, "LoopEnd")
			c.Loop = &[2]int{start, end}
		}
		cues[name] = c
	}
	return cues, nil
}
