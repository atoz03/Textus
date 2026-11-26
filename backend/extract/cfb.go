package extract

import (
	"bytes"
	"encoding/binary"
	"strings"
	"unicode/utf16"
)

// Compound files (MS-CFB), the container of an .xls (and of an encrypted
// .xlsx): a FAT file system inside one file, read here only as far as
// fetching a stream by its name. Sector chains are followed at most once
// round the table, so a crafted loop ends.

var cfbSignature = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}

const (
	cfbEndOfChain = 0xFFFFFFFE
	cfbStream     = 2
	cfbRoot       = 5
	// cfbMiniSector is the mini stream's sector size: streams under the
	// cutoff (4096 bytes) live there, in 64-byte sectors.
	cfbMiniSector = 64
)

type cfb struct {
	data    []byte
	shift   uint
	cutoff  uint64
	fat     []uint32
	miniFAT []uint32
	mini    []byte
	entries []cfbEntry
}

type cfbEntry struct {
	name  string
	kind  byte
	start uint32
	size  uint64
}

// openCFB reads a compound file's tables and directory.
func openCFB(data []byte) (*cfb, bool) {
	le := binary.LittleEndian
	if len(data) < 512 || !bytes.HasPrefix(data, cfbSignature) {
		return nil, false
	}
	c := &cfb{data: data, shift: uint(le.Uint16(data[0x1E:])), cutoff: uint64(le.Uint32(data[0x38:]))}
	if c.shift != 9 && c.shift != 12 {
		return nil, false
	}
	// The FAT's own sectors: 109 listed in the header, the rest in a
	// chain of DIFAT sectors, each ending with the next one's number.
	fatSectors := make([]uint32, 0, 109)
	for i := range 109 {
		fatSectors = append(fatSectors, le.Uint32(data[0x4C+4*i:]))
	}
	per := 1<<c.shift/4 - 1
	for s, n := le.Uint32(data[0x44:]), 0; n < len(data)>>c.shift; n++ {
		sector, ok := c.sector(s)
		if !ok || len(sector) < 4*(per+1) {
			break
		}
		for i := range per {
			fatSectors = append(fatSectors, le.Uint32(sector[4*i:]))
		}
		s = le.Uint32(sector[4*per:])
	}
	for _, s := range fatSectors[:min(int(le.Uint32(data[0x2C:])), len(fatSectors))] {
		sector, ok := c.sector(s)
		if !ok {
			break
		}
		for i := 0; i+4 <= len(sector); i += 4 {
			c.fat = append(c.fat, le.Uint32(sector[i:]))
		}
	}
	dir := c.chain(le.Uint32(data[0x30:]), c.fat, c.sector)
	for i := 0; i+128 <= len(dir); i += 128 {
		e := dir[i : i+128]
		entry := cfbEntry{kind: e[0x42], start: le.Uint32(e[0x74:]), size: le.Uint64(e[0x78:])}
		if c.shift == 9 {
			// Version 3 files may leave junk in the high half.
			entry.size &= 0xFFFFFFFF
		}
		if n := int(le.Uint16(e[0x40:])); n >= 2 && n <= 64 {
			units := make([]uint16, n/2-1)
			for j := range units {
				units[j] = le.Uint16(e[2*j:])
			}
			entry.name = string(utf16.Decode(units))
		}
		c.entries = append(c.entries, entry)
	}
	if len(c.entries) > 0 && c.entries[0].kind == cfbRoot {
		root := c.entries[0]
		c.mini = c.chain(root.start, c.fat, c.sector)
		c.mini = c.mini[:min(uint64(len(c.mini)), root.size)]
		table := c.chain(le.Uint32(data[0x3C:]), c.fat, c.sector)
		for i := 0; i+4 <= len(table); i += 4 {
			c.miniFAT = append(c.miniFAT, le.Uint32(table[i:]))
		}
	}
	return c, true
}

// stream is the stream called name (in any case), whole.
func (c *cfb) stream(name string) ([]byte, bool) {
	for _, e := range c.entries {
		if e.kind != cfbStream || !strings.EqualFold(e.name, name) {
			continue
		}
		var data []byte
		if e.size < c.cutoff {
			data = c.chain(e.start, c.miniFAT, c.miniSector)
		} else {
			data = c.chain(e.start, c.fat, c.sector)
		}
		return data[:min(uint64(len(data)), e.size)], true
	}
	return nil, false
}

// chain is the bytes of the sectors from start on, each one's next in
// table; a chain that leaves the table or loops ends there.
func (c *cfb) chain(start uint32, table []uint32, sector func(uint32) ([]byte, bool)) []byte {
	var out []byte
	for s, n := start, 0; s != cfbEndOfChain && int(s) < len(table) && n < len(table); n++ {
		data, ok := sector(s)
		if !ok {
			break
		}
		out = append(out, data...)
		s = table[s]
	}
	return out
}

// sector is sector s of the file; the header takes the first one's room.
// The last may come short.
func (c *cfb) sector(s uint32) ([]byte, bool) {
	start := (int64(s) + 1) << c.shift
	if start >= int64(len(c.data)) {
		return nil, false
	}
	return c.data[start:min(start+1<<c.shift, int64(len(c.data)))], true
}

func (c *cfb) miniSector(s uint32) ([]byte, bool) {
	start := int64(s) * cfbMiniSector
	if start >= int64(len(c.mini)) {
		return nil, false
	}
	return c.mini[start:min(start+cfbMiniSector, int64(len(c.mini)))], true
}
