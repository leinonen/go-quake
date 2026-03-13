package bsp

import (
	"math"
	"strings"
)

// Special sentinel values returned in the brightness SSBO for non-lit faces.
// The fragment shader branches on these to apply special colors.
const (
	brightnessSky   = 2.0 // sky texture → rendered blue by shader
	brightnessWater = 3.0 // water texture (*) → rendered blue by shader
)

// FaceBrightness returns one value per face for the brightness SSBO:
//   - 0.0–1.0: average lightmap brightness (normal geometry)
//   - 2.0: sky face
//   - 3.0: water face
func FaceBrightness(m *Map) []float32 {
	out := make([]float32, len(m.Faces))
	for i, face := range m.Faces {
		out[i] = faceBrightness(m, face)
	}
	return out
}

func textureName(m *Map, face DFace) string {
	if int(face.TexInfo) >= len(m.TexInfos) {
		return ""
	}
	idx := int(m.TexInfos[face.TexInfo].MipTex)
	if idx < 0 || idx >= len(m.TextureNames) {
		return ""
	}
	return m.TextureNames[idx]
}

func faceBrightness(m *Map, face DFace) float32 {
	name := strings.ToLower(textureName(m, face))
	if strings.HasPrefix(name, "sky") {
		return brightnessSky
	}
	if strings.HasPrefix(name, "*") {
		return brightnessWater
	}

	if face.LightOfs < 0 || int(face.LightOfs) >= len(m.LightData) || len(m.LightData) == 0 {
		return 1.0
	}
	if int(face.TexInfo) >= len(m.TexInfos) {
		return 1.0
	}
	ti := m.TexInfos[face.TexInfo]

	// Compute s/t extents across face vertices to determine lightmap size.
	minS, maxS := float32(math.MaxFloat32), float32(-math.MaxFloat32)
	minT, maxT := float32(math.MaxFloat32), float32(-math.MaxFloat32)

	for k := 0; k < int(face.NumEdges); k++ {
		seIdx := int(face.FirstEdge) + k
		se := m.SurfEdges[seIdx]
		var v [3]float32
		if se >= 0 {
			v = m.Vertices[m.Edges[se].V[0]]
		} else {
			v = m.Vertices[m.Edges[-se].V[1]]
		}
		s := v[0]*ti.Vecs[0][0] + v[1]*ti.Vecs[0][1] + v[2]*ti.Vecs[0][2] + ti.Vecs[0][3]
		t := v[0]*ti.Vecs[1][0] + v[1]*ti.Vecs[1][1] + v[2]*ti.Vecs[1][2] + ti.Vecs[1][3]
		if s < minS {
			minS = s
		}
		if s > maxS {
			maxS = s
		}
		if t < minT {
			minT = t
		}
		if t > maxT {
			maxT = t
		}
	}

	w := int(math.Floor(float64(maxS)/16)) - int(math.Floor(float64(minS)/16)) + 1
	h := int(math.Floor(float64(maxT)/16)) - int(math.Floor(float64(minT)/16)) + 1
	numTexels := w * h
	if numTexels <= 0 {
		return 1.0
	}

	start := int(face.LightOfs)
	end := start + numTexels*3
	if end > len(m.LightData) {
		return 1.0
	}

	var sum float32
	for j := start; j < end; j++ {
		sum += float32(m.LightData[j])
	}
	return sum / float32(numTexels*3) / 255.0
}
