package auth

import (
	"encoding/binary"
	"errors"
	"math"
)

// A minimal CBOR (RFC 8949) decoder for WebAuthn attestation objects and
// COSE keys. Integers decode to int64, byte strings to []byte, text to
// string, arrays to []any and maps to map[any]any.

var errCBOR = errors.New("invalid CBOR data")

type cborDecoder struct {
	b     []byte
	pos   int
	depth int
}

// decodeCBOR decodes one item and returns it with the number of bytes used.
func decodeCBOR(b []byte) (any, int, error) {
	d := &cborDecoder{b: b}
	v, err := d.item()
	return v, d.pos, err
}

func (d *cborDecoder) need(n int) error {
	if n < 0 || d.pos+n > len(d.b) {
		return errCBOR
	}
	return nil
}

func (d *cborDecoder) arg(info byte) (uint64, error) {
	switch {
	case info < 24:
		return uint64(info), nil
	case info == 24:
		if err := d.need(1); err != nil {
			return 0, err
		}
		d.pos++
		return uint64(d.b[d.pos-1]), nil
	case info == 25:
		if err := d.need(2); err != nil {
			return 0, err
		}
		d.pos += 2
		return uint64(binary.BigEndian.Uint16(d.b[d.pos-2:])), nil
	case info == 26:
		if err := d.need(4); err != nil {
			return 0, err
		}
		d.pos += 4
		return uint64(binary.BigEndian.Uint32(d.b[d.pos-4:])), nil
	case info == 27:
		if err := d.need(8); err != nil {
			return 0, err
		}
		d.pos += 8
		return binary.BigEndian.Uint64(d.b[d.pos-8:]), nil
	}
	return 0, errCBOR // indefinite lengths are not used by WebAuthn
}

func (d *cborDecoder) item() (any, error) {
	if d.depth++; d.depth > 16 {
		return nil, errCBOR
	}
	defer func() { d.depth-- }()
	if err := d.need(1); err != nil {
		return nil, err
	}
	ib := d.b[d.pos]
	d.pos++
	major, info := ib>>5, ib&0x1f
	if major == 7 {
		switch info {
		case 20:
			return false, nil
		case 21:
			return true, nil
		case 22, 23:
			return nil, nil
		case 25, 26, 27:
			n, err := d.arg(info)
			if info == 27 {
				return math.Float64frombits(n), err
			}
			return float64(n), err // half/single floats are not needed
		}
		return nil, errCBOR
	}
	n, err := d.arg(info)
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		if n > math.MaxInt64 {
			return nil, errCBOR
		}
		return int64(n), nil
	case 1:
		if n > math.MaxInt64 {
			return nil, errCBOR
		}
		return -1 - int64(n), nil
	case 2, 3:
		if n > uint64(len(d.b)) {
			return nil, errCBOR
		}
		if err := d.need(int(n)); err != nil {
			return nil, err
		}
		v := d.b[d.pos : d.pos+int(n)]
		d.pos += int(n)
		if major == 3 {
			return string(v), nil
		}
		return append([]byte{}, v...), nil
	case 4:
		if n > uint64(len(d.b)) {
			return nil, errCBOR
		}
		out := make([]any, 0, n)
		for i := uint64(0); i < n; i++ {
			v, err := d.item()
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 5:
		if n > uint64(len(d.b)) {
			return nil, errCBOR
		}
		out := make(map[any]any, n)
		for i := uint64(0); i < n; i++ {
			k, err := d.item()
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, errCBOR
			}
			v, err := d.item()
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case 6: // tag: skip it, keep the value
		return d.item()
	}
	return nil, errCBOR
}
