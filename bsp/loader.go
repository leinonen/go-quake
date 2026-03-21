package bsp

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
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
	TexInfos     []DTexInfo
	LightData    []byte
	TextureNames []string   // indexed by MipTex field of DTexInfo
	MipTexes     []MipTex   // indexed by MipTex field of DTexInfo
	Entities     string     // raw entity lump text
	ClipNodes    []DClipNode
	Hull0        []DClipNode // point hull built from rendering nodes at load time
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

	if l := header.Lumps[LumpEntities]; l.Length > 0 {
		buf := make([]byte, l.Length)
		if _, err := f.ReadAt(buf, int64(l.Offset)); err != nil {
			return nil, fmt.Errorf("read entities: %w", err)
		}
		// Strip trailing null byte if present.
		m.Entities = string(bytes.TrimRight(buf, "\x00"))
	}

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

	if buf, err := readLump(LumpClipNodes, 8); err != nil {
		return nil, err
	} else {
		m.ClipNodes = make([]DClipNode, len(buf)/8)
		readStructSlice(buf, m.ClipNodes)
	}

	if buf, err := readLump(LumpModels, 64); err != nil {
		return nil, err
	} else {
		m.Models = make([]DModel, len(buf)/64)
		readStructSlice(buf, m.Models)
	}

	if buf, err := readLump(LumpTexInfo, 40); err != nil {
		return nil, err
	} else {
		m.TexInfos = make([]DTexInfo, len(buf)/40)
		readStructSlice(buf, m.TexInfos)
	}

	if l := header.Lumps[LumpLighting]; l.Length > 0 {
		m.LightData = make([]byte, l.Length)
		if _, err := f.ReadAt(m.LightData, int64(l.Offset)); err != nil {
			return nil, fmt.Errorf("read lighting: %w", err)
		}
	}

	// Parse miptex entries from lump 2.
	// Layout: int32 count, int32 offsets[count], then at each offset:
	//   char name[16], uint32 width, uint32 height, uint32 offsets[4], then pixel data
	if l := header.Lumps[LumpTextures]; l.Length >= 4 {
		raw := make([]byte, l.Length)
		if _, err := f.ReadAt(raw, int64(l.Offset)); err != nil {
			return nil, fmt.Errorf("read textures: %w", err)
		}
		count := int(int32(raw[0]) | int32(raw[1])<<8 | int32(raw[2])<<16 | int32(raw[3])<<24)
		m.TextureNames = make([]string, count)
		m.MipTexes = make([]MipTex, count)
		for i := 0; i < count; i++ {
			offIdx := 4 + i*4
			if offIdx+4 > len(raw) {
				break
			}
			off := int(int32(raw[offIdx]) | int32(raw[offIdx+1])<<8 | int32(raw[offIdx+2])<<16 | int32(raw[offIdx+3])<<24)
			if off < 0 || off+40 > len(raw) {
				continue
			}
			// name[16], width(4), height(4), mip0offset(4), ...
			name := raw[off : off+16]
			end := 0
			for end < 16 && name[end] != 0 {
				end++
			}
			texName := string(name[:end])
			m.TextureNames[i] = texName

			w := int(uint32(raw[off+16]) | uint32(raw[off+17])<<8 | uint32(raw[off+18])<<16 | uint32(raw[off+19])<<24)
			h := int(uint32(raw[off+20]) | uint32(raw[off+21])<<8 | uint32(raw[off+22])<<16 | uint32(raw[off+23])<<24)
			mip0Off := int(uint32(raw[off+24]) | uint32(raw[off+25])<<8 | uint32(raw[off+26])<<16 | uint32(raw[off+27])<<24)

			mt := MipTex{Name: texName, Width: w, Height: h}
			pixStart := off + mip0Off
			pixSize := w * h
			if w > 0 && h > 0 && mip0Off > 0 && pixStart >= 0 && pixStart+pixSize <= len(raw) {
				mt.Pixels = make([]byte, pixSize)
				copy(mt.Pixels, raw[pixStart:pixStart+pixSize])
			}
			m.MipTexes[i] = mt
		}
	}

	// Build hull 0 (point hull) from BSP rendering nodes, mirroring Quake's Mod_MakeHull0.
	// BSP node children encode leaf indices as -(leafIdx+1); convert to leaf Contents values.
	m.Hull0 = make([]DClipNode, len(m.Nodes))
	for i, n := range m.Nodes {
		m.Hull0[i].PlaneNum = n.PlaneNum
		for j := 0; j < 2; j++ {
			child := int(n.Children[j])
			if child >= 0 {
				m.Hull0[i].Children[j] = n.Children[j]
			} else {
				leafIdx := -(child + 1)
				if leafIdx < len(m.Leaves) {
					m.Hull0[i].Children[j] = int16(m.Leaves[leafIdx].Contents)
				} else {
					m.Hull0[i].Children[j] = int16(ContentsEmpty)
				}
			}
		}
	}

	return m, nil
}

// SpawnPoint parses the entity lump and returns the origin of the first
// info_player_start entity, or ok=false if none is found.
func (m *Map) SpawnPoint() (origin [3]float32, ok bool) {
	// Entity lump is a series of { key "val" key "val" } blocks.
	text := m.Entities
	for {
		start := strings.Index(text, "{")
		end := strings.Index(text, "}")
		if start < 0 || end < 0 || end < start {
			break
		}
		block := text[start+1 : end]
		text = text[end+1:]

		if !strings.Contains(block, "info_player_start") {
			continue
		}
		// Parse "origin" key.
		const key = `"origin"`
		ki := strings.Index(block, key)
		if ki < 0 {
			continue
		}
		rest := block[ki+len(key):]
		// Skip whitespace, then read quoted value.
		rest = strings.TrimLeft(rest, " \t\r\n")
		if len(rest) == 0 || rest[0] != '"' {
			continue
		}
		rest = rest[1:]
		qend := strings.Index(rest, `"`)
		if qend < 0 {
			continue
		}
		val := rest[:qend]
		var x, y, z float32
		if n, _ := fmt.Sscanf(val, "%f %f %f", &x, &y, &z); n == 3 {
			return [3]float32{x, y, z}, true
		}
	}
	return [3]float32{}, false
}

func readStructSlice(buf []byte, dst any) {
	_ = binary.Read(bytes.NewReader(buf), binary.LittleEndian, dst)
}

// seqReader wraps a ReadSeeker for sequential binary.Read use.
type seqReader struct{ r io.ReadSeeker }

func newSeqReader(r io.ReadSeeker) *seqReader { return &seqReader{r} }
func (s *seqReader) Read(p []byte) (int, error) { return s.r.Read(p) }
