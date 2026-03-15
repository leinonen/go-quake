package bsp

import (
	"math"
	"strings"
)

// Special sentinel values returned in the brightness SSBO for non-lit faces.
// The fragment shader branches on these to apply special colors.
const (
	brightnessSky    = 2.0 // sky texture → rendered by skybox shader
	brightnessWater  = 3.0 // water texture (*) → procedural water
	brightnessLava   = 4.0 // lava texture (*lava*) → procedural lava
	brightnessPortal = 5.0 // teleporter texture (*teleport) → procedural portal
)

// LightmapFaceInfo describes where a face's lightmap data lives in the atlas.
type LightmapFaceInfo struct {
	AtlasX int     // texel X origin in atlas (inside 1-texel padding)
	AtlasY int     // texel Y origin in atlas
	W      int     // lightmap width in texels (>= 1)
	H      int     // lightmap height in texels (>= 1)
	MinS   float32 // floor(minS/16)*16 — lightmap space S origin
	MinT   float32 // floor(minT/16)*16 — lightmap space T origin
}

// BuildLightmapAtlas packs all face lightmaps into a single RGB atlas texture.
// Faces without valid lightmap data (sky, water, LightOfs<0) map to a grey fallback texel at (0,0).
func BuildLightmapAtlas(m *Map) (pixels []byte, atlasW, atlasH int, infos []LightmapFaceInfo) {
	atlasW = 2048
	infos = make([]LightmapFaceInfo, len(m.Faces))

	// Shelf-pack cursor; start at x=2 to reserve (0,0) for fallback.
	curX, curY, rowH := 2, 0, 0

	for i, face := range m.Faces {
		name := strings.ToLower(textureName(m, face))
		if strings.HasPrefix(name, "sky") || strings.HasPrefix(name, "*") { // water, lava, slime all start with *
			// sentinel faces: point at fallback texel
			continue
		}
		if face.LightOfs < 0 || len(m.LightData) == 0 || int(face.LightOfs) >= len(m.LightData) {
			continue
		}
		if int(face.TexInfo) >= len(m.TexInfos) {
			continue
		}
		ti := m.TexInfos[face.TexInfo]

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

		lmOriginS := float32(math.Floor(float64(minS)/16) * 16)
		lmOriginT := float32(math.Floor(float64(minT)/16) * 16)
		lmW := int(math.Floor(float64(maxS)/16)) - int(math.Floor(float64(minS)/16)) + 1
		lmH := int(math.Floor(float64(maxT)/16)) - int(math.Floor(float64(minT)/16)) + 1
		if lmW < 1 {
			lmW = 1
		}
		if lmH < 1 {
			lmH = 1
		}
		if int(face.LightOfs)+lmW*lmH > len(m.LightData) {
			continue
		}

		// Shelf-pack: advance row if this lightmap doesn't fit.
		if curX+lmW+2 > atlasW {
			curY += rowH
			curX = 0
			rowH = 0
		}
		infos[i] = LightmapFaceInfo{
			AtlasX: curX + 1,
			AtlasY: curY + 1,
			W:      lmW,
			H:      lmH,
			MinS:   lmOriginS,
			MinT:   lmOriginT,
		}
		curX += lmW + 2
		if lmH+2 > rowH {
			rowH = lmH + 2
		}
	}

	rawH := curY + rowH
	atlasH = 2
	for atlasH < rawH {
		atlasH <<= 1
	}

	pixels = make([]byte, atlasW*atlasH*3)
	// Fallback texel at (0,0): 128,128,128 → *2.0 in shader = 1.0 full brightness
	pixels[0] = 128
	pixels[1] = 128
	pixels[2] = 128

	// Copy each face's lightmap rows into the atlas.
	// BSP29 lightmaps are 1 byte per texel (grayscale luminance); replicate to RGB.
	for i, face := range m.Faces {
		info := infos[i]
		if info.W == 0 || info.H == 0 {
			continue
		}
		if face.LightOfs < 0 {
			continue
		}
		src := int(face.LightOfs)
		for row := 0; row < info.H; row++ {
			for col := 0; col < info.W; col++ {
				dstX := info.AtlasX + col
				dstY := info.AtlasY + row
				dst := (dstY*atlasW + dstX) * 3
				lum := m.LightData[src+row*info.W+col]
				pixels[dst+0] = lum
				pixels[dst+1] = lum
				pixels[dst+2] = lum
			}
		}
	}
	return
}

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
	if strings.HasPrefix(name, "*lava") {
		return brightnessLava
	}
	if strings.HasPrefix(name, "*tele") {
		return brightnessPortal
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
	end := start + numTexels
	if end > len(m.LightData) {
		return 1.0
	}

	var sum float32
	for j := start; j < end; j++ {
		sum += float32(m.LightData[j])
	}
	return sum / float32(numTexels) / 255.0
}
