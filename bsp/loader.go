package bsp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

// Map holds all parsed BSP data.
type Map struct {
	Planes       []DPlane
	Vertices     [][3]float32
	VisData      []byte
	Nodes        []DNode
	Faces        []DFace
	Leaves       []DLeaf
	MarkSurfaces []uint16
	Edges        []DEdge
	SurfEdges    []int32
	Models       []DModel
}

// Load parses a BSP29 file from disk path.
func Load(path string) (*Map, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open bsp: %w", err)
	}
	defer f.Close()
	return parse(f)
}

// LoadBytes parses a BSP29 from an in-memory buffer (e.g. extracted from PAK).
func LoadBytes(data []byte) (*Map, error) {
	return parse(bytes.NewReader(data))
}

// parse reads a BSP29 from any ReaderAt+ReadSeeker.
func parse(f interface {
	io.ReaderAt
	io.ReadSeeker
}) (*Map, error) {
	var header DHeader
	if err := binary.Read(newSeqReader(f), binary.LittleEndian, &header); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	if header.Version != BSPVersion {
		return nil, fmt.Errorf("unsupported BSP version %d (want %d)", header.Version, BSPVersion)
	}

	readLump := func(lump int, elemSize int) ([]byte, error) {
		l := header.Lumps[lump]
		if l.Length == 0 {
			return nil, nil
		}
		if int(l.Length)%elemSize != 0 {
			return nil, fmt.Errorf("lump %d length %d not divisible by %d", lump, l.Length, elemSize)
		}
		buf := make([]byte, l.Length)
		if _, err := f.ReadAt(buf, int64(l.Offset)); err != nil {
			return nil, fmt.Errorf("read lump %d: %w", lump, err)
		}
		return buf, nil
	}

	m := &Map{}

	if buf, err := readLump(LumpPlanes, 20); err != nil {
		return nil, err
	} else {
		m.Planes = make([]DPlane, len(buf)/20)
		readStructSlice(buf, m.Planes)
	}

	if buf, err := readLump(LumpVertices, 12); err != nil {
		return nil, err
	} else {
		m.Vertices = make([][3]float32, len(buf)/12)
		readStructSlice(buf, m.Vertices)
	}

	if l := header.Lumps[LumpVisibility]; l.Length > 0 {
		m.VisData = make([]byte, l.Length)
		if _, err := f.ReadAt(m.VisData, int64(l.Offset)); err != nil {
			return nil, fmt.Errorf("read vis: %w", err)
		}
	}

	if buf, err := readLump(LumpNodes, 24); err != nil {
		return nil, err
	} else {
		m.Nodes = make([]DNode, len(buf)/24)
		readStructSlice(buf, m.Nodes)
	}

	if buf, err := readLump(LumpFaces, 20); err != nil {
		return nil, err
	} else {
		m.Faces = make([]DFace, len(buf)/20)
		readStructSlice(buf, m.Faces)
	}

	if buf, err := readLump(LumpLeaves, 28); err != nil {
		return nil, err
	} else {
		m.Leaves = make([]DLeaf, len(buf)/28)
		readStructSlice(buf, m.Leaves)
	}

	if buf, err := readLump(LumpMarkSurfaces, 2); err != nil {
		return nil, err
	} else {
		m.MarkSurfaces = make([]uint16, len(buf)/2)
		readStructSlice(buf, m.MarkSurfaces)
	}

	if buf, err := readLump(LumpEdges, 4); err != nil {
		return nil, err
	} else {
		m.Edges = make([]DEdge, len(buf)/4)
		readStructSlice(buf, m.Edges)
	}

	if buf, err := readLump(LumpSurfEdges, 4); err != nil {
		return nil, err
	} else {
		m.SurfEdges = make([]int32, len(buf)/4)
		readStructSlice(buf, m.SurfEdges)
	}

	if buf, err := readLump(LumpModels, 64); err != nil {
		return nil, err
	} else {
		m.Models = make([]DModel, len(buf)/64)
		readStructSlice(buf, m.Models)
	}

	return m, nil
}

func readStructSlice(buf []byte, dst any) {
	_ = binary.Read(bytes.NewReader(buf), binary.LittleEndian, dst)
}

// seqReader wraps a ReadSeeker for sequential binary.Read use.
type seqReader struct{ r io.ReadSeeker }

func newSeqReader(r io.ReadSeeker) *seqReader { return &seqReader{r} }
func (s *seqReader) Read(p []byte) (int, error) { return s.r.Read(p) }
