// Package pak reads id Software PAK archives (Quake 1 format).
package pak

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

const magic = "PACK"

type header struct {
	Magic  [4]byte
	Offset int32
	Size   int32
}

type entry struct {
	Name   [56]byte
	Offset int32
	Size   int32
}

// PAK holds an open PAK file and its directory.
type PAK struct {
	f       *os.File
	entries []entry
}

// Open opens a PAK file for reading.
func Open(path string) (*PAK, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	var hdr header
	if err := binary.Read(f, binary.LittleEndian, &hdr); err != nil {
		f.Close()
		return nil, fmt.Errorf("read pak header: %w", err)
	}
	if string(hdr.Magic[:]) != magic {
		f.Close()
		return nil, fmt.Errorf("not a PAK file (got %q)", hdr.Magic)
	}

	numEntries := int(hdr.Size) / 64
	entries := make([]entry, numEntries)
	if _, err := f.ReadAt(make([]byte, 0), int64(hdr.Offset)); err != nil {
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(int64(hdr.Offset), io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	if err := binary.Read(f, binary.LittleEndian, &entries); err != nil {
		f.Close()
		return nil, fmt.Errorf("read pak directory: %w", err)
	}

	return &PAK{f: f, entries: entries}, nil
}

// Close closes the PAK file.
func (p *PAK) Close() { p.f.Close() }

// List returns all file names in the PAK.
func (p *PAK) List() []string {
	names := make([]string, len(p.entries))
	for i, e := range p.entries {
		names[i] = entryName(e)
	}
	return names
}

// ReadFile returns the contents of the named file (case-insensitive).
func (p *PAK) ReadFile(name string) ([]byte, error) {
	name = strings.ToLower(name)
	for _, e := range p.entries {
		if strings.ToLower(entryName(e)) == name {
			buf := make([]byte, e.Size)
			if _, err := p.f.ReadAt(buf, int64(e.Offset)); err != nil {
				return nil, fmt.Errorf("read %s: %w", name, err)
			}
			return buf, nil
		}
	}
	return nil, fmt.Errorf("%q not found in PAK", name)
}

// FindMaps returns all .bsp paths in the PAK.
func (p *PAK) FindMaps() []string {
	var maps []string
	for _, e := range p.entries {
		n := strings.ToLower(entryName(e))
		if strings.HasPrefix(n, "maps/") && strings.HasSuffix(n, ".bsp") {
			maps = append(maps, entryName(e))
		}
	}
	return maps
}

// NewReader returns an io.Reader over the named file.
func (p *PAK) NewReader(name string) (io.Reader, error) {
	data, err := p.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func entryName(e entry) string {
	n := e.Name[:]
	if i := bytes.IndexByte(n, 0); i >= 0 {
		n = n[:i]
	}
	return string(n)
}
