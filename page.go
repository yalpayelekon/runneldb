package runneldb

import (
	"encoding/binary"
	"fmt"
)

const pageSize = 4096

// Page is a fixed-size slotted page used to pack row payloads.
type Page struct {
	buf [pageSize]byte
}

// page layout:
//   [0:2]  slot count (uint16 LE)
//   [2:4]  free offset (uint16 LE) — next byte available for payload growth from low addresses
//   slots grow downward from the end: each slot is uint16 offset + uint16 length
//   payloads grow upward from offset 4

func (p *Page) reset() {
	for i := range p.buf {
		p.buf[i] = 0
	}
	binary.LittleEndian.PutUint16(p.buf[2:4], 4) // free starts after header
}

func (p *Page) slotCount() int {
	return int(binary.LittleEndian.Uint16(p.buf[0:2]))
}

func (p *Page) freeOff() int {
	return int(binary.LittleEndian.Uint16(p.buf[2:4]))
}

func (p *Page) setSlotCount(n int) {
	binary.LittleEndian.PutUint16(p.buf[0:2], uint16(n))
}

func (p *Page) setFreeOff(n int) {
	binary.LittleEndian.PutUint16(p.buf[2:4], uint16(n))
}

func (p *Page) slotDirStart() int {
	// slot directory grows from the end of the page
	return pageSize - p.slotCount()*4
}

// Put appends a payload as a new slot. Returns ErrRowTooLarge if it does not fit.
func (p *Page) Put(payload []byte) error {
	n := p.slotCount()
	free := p.freeOff()
	dirStart := pageSize - (n+1)*4
	need := len(payload)
	if free+need > dirStart {
		return ErrRowTooLarge
	}
	copy(p.buf[free:free+need], payload)
	off := free
	binary.LittleEndian.PutUint16(p.buf[dirStart:dirStart+2], uint16(off))
	binary.LittleEndian.PutUint16(p.buf[dirStart+2:dirStart+4], uint16(need))
	p.setSlotCount(n + 1)
	p.setFreeOff(free + need)
	return nil
}

// Get returns the payload at slot index.
func (p *Page) Get(i int) ([]byte, error) {
	n := p.slotCount()
	if i < 0 || i >= n {
		return nil, fmt.Errorf("%w: slot %d out of range", ErrSQL, i)
	}
	dir := pageSize - (i+1)*4
	off := int(binary.LittleEndian.Uint16(p.buf[dir : dir+2]))
	length := int(binary.LittleEndian.Uint16(p.buf[dir+2 : dir+4]))
	out := make([]byte, length)
	copy(out, p.buf[off:off+length])
	return out, nil
}

// Bytes returns a copy of the page buffer.
func (p *Page) Bytes() []byte {
	return append([]byte(nil), p.buf[:]...)
}

// LoadPage copies raw bytes into a page.
func LoadPage(raw []byte) (*Page, error) {
	if len(raw) != pageSize {
		return nil, fmt.Errorf("%w: page must be %d bytes", ErrSQL, pageSize)
	}
	p := &Page{}
	copy(p.buf[:], raw)
	return p, nil
}

// EncodeRowPage packs a single row payload into a fresh page.
func EncodeRowPage(rowPayload []byte) ([]byte, error) {
	var p Page
	p.reset()
	if err := p.Put(rowPayload); err != nil {
		return nil, err
	}
	return p.Bytes(), nil
}

// DecodeRowPage extracts the first slot payload from a page image.
func DecodeRowPage(raw []byte) ([]byte, error) {
	p, err := LoadPage(raw)
	if err != nil {
		return nil, err
	}
	if p.slotCount() < 1 {
		return nil, fmt.Errorf("%w: empty row page", ErrSQL)
	}
	return p.Get(0)
}
