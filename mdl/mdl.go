// Package mdl parses Quake 1 MDL (alias model) files (IDPO version 6).
package mdl

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

const (
	idPolyHeader = 0x4F504449 // "IDPO"
	mdlVersion   = 6
)

type header struct {
	Ident      int32
	Version    int32
	Scale      [3]float32
	Origin     [3]float32
	Radius     float32
	EyePos     [3]float32
	NumSkins   int32
	SkinWidth  int32
	SkinHeight int32
	NumVerts   int32
	NumTris    int32
	NumFrames  int32
	SyncType   int32
	Flags      int32
	Size       float32
}

type texCoord struct {
	OnSeam int32
	S      int32
	T      int32
}

type triangle struct {
	FacesFront int32
	Verts      [3]int32
}

type trivertx struct {
	V           [3]byte
	NormalIndex byte
}

// MDL holds parsed geometry and skin data from a Quake MDL file.
type MDL struct {
	hdr        header
	SkinWidth  int
	SkinHeight int
	skins      [][]byte    // palette-indexed skin pixel data (one per skin)
	texCoords  []texCoord
	triangles  []triangle
	frames     [][][3]byte // [frameIdx][vertIdx] = packed [3]byte coords
}

// Load parses a Quake MDL file from raw bytes.
func Load(data []byte) (*MDL, error) {
	r := bytes.NewReader(data)

	var hdr header
	if err := binary.Read(r, binary.LittleEndian, &hdr); err != nil {
		return nil, fmt.Errorf("mdl header: %w", err)
	}
	if uint32(hdr.Ident) != idPolyHeader {
		return nil, fmt.Errorf("not an MDL file (ident 0x%08x)", uint32(hdr.Ident))
	}
	if hdr.Version != mdlVersion {
		return nil, fmt.Errorf("unsupported MDL version %d", hdr.Version)
	}

	m := &MDL{
		hdr:        hdr,
		SkinWidth:  int(hdr.SkinWidth),
		SkinHeight: int(hdr.SkinHeight),
	}

	skinSize := m.SkinWidth * m.SkinHeight

	// Skins
	for i := 0; i < int(hdr.NumSkins); i++ {
		var skinType int32
		if err := binary.Read(r, binary.LittleEndian, &skinType); err != nil {
			return nil, fmt.Errorf("skin %d type: %w", i, err)
		}
		if skinType == 0 {
			// single skin
			buf := make([]byte, skinSize)
			if _, err := r.Read(buf); err != nil {
				return nil, fmt.Errorf("skin %d data: %w", i, err)
			}
			m.skins = append(m.skins, buf)
		} else {
			// group skin — read count, skip intervals, keep first frame
			var nf int32
			binary.Read(r, binary.LittleEndian, &nf)
			r.Seek(int64(nf)*4, 1) // skip float32 intervals
			var first []byte
			for j := int32(0); j < nf; j++ {
				buf := make([]byte, skinSize)
				r.Read(buf)
				if j == 0 {
					first = buf
				}
			}
			m.skins = append(m.skins, first)
		}
	}

	// Texture coordinates
	m.texCoords = make([]texCoord, hdr.NumVerts)
	for i := range m.texCoords {
		if err := binary.Read(r, binary.LittleEndian, &m.texCoords[i]); err != nil {
			return nil, fmt.Errorf("texcoord %d: %w", i, err)
		}
	}

	// Triangles
	m.triangles = make([]triangle, hdr.NumTris)
	for i := range m.triangles {
		if err := binary.Read(r, binary.LittleEndian, &m.triangles[i]); err != nil {
			return nil, fmt.Errorf("triangle %d: %w", i, err)
		}
	}

	// Frames
	for i := 0; i < int(hdr.NumFrames); i++ {
		var frameType int32
		if err := binary.Read(r, binary.LittleEndian, &frameType); err != nil {
			return nil, fmt.Errorf("frame %d type: %w", i, err)
		}
		if frameType == 0 {
			if err := readSingleFrame(r, m); err != nil {
				return nil, fmt.Errorf("frame %d: %w", i, err)
			}
		} else {
			// group frame — read bboxes, intervals, subframes; keep first
			var nf int32
			binary.Read(r, binary.LittleEndian, &nf)
			var bboxMin, bboxMax trivertx
			binary.Read(r, binary.LittleEndian, &bboxMin)
			binary.Read(r, binary.LittleEndian, &bboxMax)
			r.Seek(int64(nf)*4, 1) // skip float32 intervals
			for j := int32(0); j < nf; j++ {
				if j == 0 {
					if err := readSingleFrame(r, m); err != nil {
						return nil, fmt.Errorf("frame %d subframe 0: %w", i, err)
					}
				} else {
					// skip: bboxmin(4) + bboxmax(4) + name(16) + numVerts*trivertx(4 each)
					r.Seek(int64(4+4+16+int(hdr.NumVerts)*4), 1)
				}
			}
		}
	}

	return m, nil
}

func readSingleFrame(r *bytes.Reader, m *MDL) error {
	var bboxMin, bboxMax trivertx
	if err := binary.Read(r, binary.LittleEndian, &bboxMin); err != nil {
		return err
	}
	if err := binary.Read(r, binary.LittleEndian, &bboxMax); err != nil {
		return err
	}
	var name [16]byte
	if _, err := r.Read(name[:]); err != nil {
		return err
	}
	frame := make([][3]byte, m.hdr.NumVerts)
	for j := range frame {
		var tv trivertx
		if err := binary.Read(r, binary.LittleEndian, &tv); err != nil {
			return fmt.Errorf("vert %d: %w", j, err)
		}
		frame[j] = tv.V
	}
	m.frames = append(m.frames, frame)
	return nil
}

// BuildVerts returns interleaved x,y,z,u,v vertex data for one animation frame.
// Produces 3 vertices per triangle (no index buffer), 5 floats each.
func (m *MDL) BuildVerts(frameIdx int) []float32 {
	if len(m.frames) == 0 {
		return nil
	}
	if frameIdx < 0 || frameIdx >= len(m.frames) {
		frameIdx = 0
	}
	packed := m.frames[frameIdx]
	h := m.hdr
	sw := float32(m.SkinWidth)
	sh := float32(m.SkinHeight)

	verts := make([]float32, 0, len(m.triangles)*3*5)
	for _, tri := range m.triangles {
		for _, vi := range tri.Verts {
			p := packed[vi]
			x := float32(p[0])*h.Scale[0] + h.Origin[0]
			y := float32(p[1])*h.Scale[1] + h.Origin[1]
			z := float32(p[2])*h.Scale[2] + h.Origin[2]

			tc := m.texCoords[vi]
			s := float32(tc.S)
			// Back-face seam correction: shift UV into second half of skin
			if tri.FacesFront == 0 && tc.OnSeam != 0 {
				s += float32(m.SkinWidth) / 2
			}
			u := (s + 0.5) / sw
			v := (float32(tc.T) + 0.5) / sh

			verts = append(verts, x, y, z, u, v)
		}
	}
	return verts
}

// NumFrames returns the number of animation frames in the MDL.
func (m *MDL) NumFrames() int { return len(m.frames) }

// SkinRGB converts palette-indexed skin pixels to packed RGB bytes.
// palette must be at least 768 bytes (256 × RGB triplets).
func (m *MDL) SkinRGB(skinIdx int, palette []byte) []byte {
	if len(m.skins) == 0 {
		return nil
	}
	if skinIdx < 0 || skinIdx >= len(m.skins) {
		skinIdx = 0
	}
	src := m.skins[skinIdx]
	dst := make([]byte, len(src)*3)
	for i, idx := range src {
		base := int(idx) * 3
		if base+2 < len(palette) {
			dst[i*3+0] = palette[base+0]
			dst[i*3+1] = palette[base+1]
			dst[i*3+2] = palette[base+2]
		}
	}
	return dst
}
