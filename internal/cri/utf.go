// Package cri reads CRI ADX2 cue sheets (ACB): the @UTF tables they are made
// of, and the per-cue concurrency limit and waveform loop of a cue sheet.
package cri

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Table is one @UTF table. Row values are uint64 (unsigned), int64
// (signed), float64, string or []byte (variable-length data, often a nested
// table).
type Table struct {
	Name    string
	Columns []string
	Rows    []map[string]any
}

const (
	flagName     = 0x10
	flagDefault  = 0x20
	flagRow      = 0x40
	typeMask     = 0x0F
	typeU8       = 0
	typeS8       = 1
	typeU16      = 2
	typeS16      = 3
	typeU32      = 4
	typeS32      = 5
	typeU64      = 6
	typeS64      = 7
	typeFloat    = 8
	typeDouble   = 9
	typeString   = 0xA
	typeVLData   = 0xB
	headerLength = 8 // "@UTF" + table size; offsets are relative to its end
)

// ParseTable reads an @UTF table.
func ParseTable(b []byte) (*Table, error) {
	if len(b) < 0x20 || string(b[:4]) != "@UTF" {
		return nil, errors.New("not an @UTF table")
	}
	size := int(binary.BigEndian.Uint32(b[4:]))
	if headerLength+size > len(b) {
		return nil, fmt.Errorf("@UTF table of %d bytes truncated to %d", size, len(b)-headerLength)
	}
	body := b[headerLength : headerLength+size]
	rowsOffset := int(binary.BigEndian.Uint16(body[2:]))
	stringsOffset := int(binary.BigEndian.Uint32(body[4:]))
	dataOffset := int(binary.BigEndian.Uint32(body[8:]))
	nameOffset := int(binary.BigEndian.Uint32(body[12:]))
	numColumns := int(binary.BigEndian.Uint16(body[16:]))
	rowWidth := int(binary.BigEndian.Uint16(body[18:]))
	numRows := int(binary.BigEndian.Uint32(body[20:]))
	if rowsOffset > len(body) || stringsOffset > len(body) || dataOffset > len(body) {
		return nil, errors.New("@UTF offsets out of range")
	}
	str := func(off int) (string, error) {
		start := stringsOffset + off
		if start < 0 || start >= len(body) {
			return "", fmt.Errorf("@UTF string offset %d out of range", off)
		}
		end := start
		for end < len(body) && body[end] != 0 {
			end++
		}
		return string(body[start:end]), nil
	}
	name, err := str(nameOffset)
	if err != nil {
		return nil, err
	}
	t := &Table{Name: name}

	// value reads one value of type typ at p and returns it and its size.
	value := func(typ byte, p int) (any, int, error) {
		need := map[byte]int{typeU8: 1, typeS8: 1, typeU16: 2, typeS16: 2, typeU32: 4, typeS32: 4, typeU64: 8, typeS64: 8,
			typeFloat: 4, typeDouble: 8, typeString: 4, typeVLData: 8}[typ]
		if need == 0 {
			return nil, 0, fmt.Errorf("@UTF column type %#x", typ)
		}
		if p+need > len(body) {
			return nil, 0, errors.New("@UTF value out of range")
		}
		v := body[p:]
		switch typ {
		case typeU8:
			return uint64(v[0]), 1, nil
		case typeS8:
			return int64(int8(v[0])), 1, nil
		case typeU16:
			return uint64(binary.BigEndian.Uint16(v)), 2, nil
		case typeS16:
			return int64(int16(binary.BigEndian.Uint16(v))), 2, nil
		case typeU32:
			return uint64(binary.BigEndian.Uint32(v)), 4, nil
		case typeS32:
			return int64(int32(binary.BigEndian.Uint32(v))), 4, nil
		case typeU64:
			return binary.BigEndian.Uint64(v), 8, nil
		case typeS64:
			return int64(binary.BigEndian.Uint64(v)), 8, nil
		case typeFloat:
			return float64(math.Float32frombits(binary.BigEndian.Uint32(v))), 4, nil
		case typeDouble:
			return math.Float64frombits(binary.BigEndian.Uint64(v)), 8, nil
		case typeString:
			s, err := str(int(binary.BigEndian.Uint32(v)))
			return s, 4, err
		default: // typeVLData
			off, n := dataOffset+int(binary.BigEndian.Uint32(v)), int(binary.BigEndian.Uint32(v[4:]))
			if n == 0 {
				return []byte{}, 8, nil
			}
			if off < 0 || off+n > len(body) {
				return nil, 0, errors.New("@UTF data out of range")
			}
			return body[off : off+n], 8, nil
		}
	}

	type column struct {
		name     string
		typ      byte
		row      bool
		constant any
	}
	columns := make([]column, numColumns)
	p := 24
	for i := range columns {
		if p+1 > len(body) {
			return nil, errors.New("@UTF columns out of range")
		}
		flags := body[p]
		p++
		c := column{typ: flags & typeMask, row: flags&flagRow != 0}
		if flags&flagName != 0 {
			if p+4 > len(body) {
				return nil, errors.New("@UTF column name out of range")
			}
			if c.name, err = str(int(binary.BigEndian.Uint32(body[p:]))); err != nil {
				return nil, err
			}
			p += 4
		}
		if flags&flagDefault != 0 {
			v, n, err := value(c.typ, p)
			if err != nil {
				return nil, err
			}
			c.constant = v
			p += n
		}
		columns[i] = c
		t.Columns = append(t.Columns, c.name)
	}
	for r := 0; r < numRows; r++ {
		row := map[string]any{}
		q := rowsOffset + r*rowWidth
		for _, c := range columns {
			if !c.row {
				if c.constant != nil {
					row[c.name] = c.constant
				}
				continue
			}
			v, n, err := value(c.typ, q)
			if err != nil {
				return nil, fmt.Errorf("@UTF %s row %d column %s: %w", name, r, c.name, err)
			}
			row[c.name] = v
			q += n
		}
		t.Rows = append(t.Rows, row)
	}
	return t, nil
}

// Uint returns an unsigned or signed integer column value as int.
func Uint(row map[string]any, column string) (int, bool) {
	switch v := row[column].(type) {
	case uint64:
		return int(v), true
	case int64:
		return int(v), true
	}
	return 0, false
}

// Nested returns a variable-length column parsed as a nested @UTF table; an
// empty column gives an empty table.
func Nested(row map[string]any, column string) (*Table, error) {
	b, ok := row[column].([]byte)
	if !ok {
		return nil, fmt.Errorf("column %s is not data", column)
	}
	if len(b) == 0 {
		return &Table{Name: column}, nil
	}
	return ParseTable(b)
}
