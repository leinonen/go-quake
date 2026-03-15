// Package pak reads id Software PAK archives (Quake 1 format).
package pak

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// MultiPAK searches multiple PAK files in reverse order so later paks override earlier ones.
type MultiPAK struct {
	paks []*PAK
}

// OpenDir opens all pak*.pak files found in dir, sorted so pak1 overrides pak0.
func OpenDir(dir string) (*MultiPAK, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "pak*.pak"))
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no pak*.pak files found in %s", dir)
	}
	m := &MultiPAK{}
	for _, path := range matches {
		p, err := Open(path)
		if err != nil {
			m.Close()
			return nil, fmt.Errorf("open %s: %w", path, err)
		}
		m.paks = append(m.paks, p)
	}
	return m, nil
}

// Close closes all PAK files.
func (m *MultiPAK) Close() {
	for _, p := range m.paks {
		p.Close()
	}
}

// ReadFile returns the contents of the named file, searching later paks first.
func (m *MultiPAK) ReadFile(name string) ([]byte, error) {
	for i := len(m.paks) - 1; i >= 0; i-- {
		if data, err := m.paks[i].ReadFile(name); err == nil {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%q not found in any PAK", name)
}

// List returns all file names across all paks (later paks last, no dedup).
func (m *MultiPAK) List() []string {
	var names []string
	for _, p := range m.paks {
		names = append(names, p.List()...)
	}
	return names
}

// FindMaps returns all .bsp paths across all paks, with later paks taking precedence.
func (m *MultiPAK) FindMaps() []string {
	seen := map[string]bool{}
	var maps []string
	for i := len(m.paks) - 1; i >= 0; i-- {
		for _, name := range m.paks[i].FindMaps() {
			lower := strings.ToLower(name)
			if !seen[lower] {
				seen[lower] = true
				maps = append(maps, name)
			}
		}
	}
	sort.Strings(maps)
	return maps
}
