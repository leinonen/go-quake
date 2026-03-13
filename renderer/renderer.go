package renderer

import (
	"fmt"
	"strings"
	"unsafe"

	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/game"
)

// Renderer owns all GL state and draws the world.
type Renderer struct {
	worldProg   uint32
	vao         uint32
	vbo         uint32
	numVerts    int32

	compute          *ComputeState
	usePVS           bool
	numFaces         uint32
	ssboBrightness   uint32
	ssboFaceAtlas    uint32
	atlasTexture     uint32
	atlasW, atlasH   int32

	mvpLoc         int32
	usePVSLoc      int32
	totalFaceLoc   int32
	atlasLoc       int32
	atlasSizeLoc   int32
}

// Init initialises GL state and uploads BSP geometry.
// palette is 768 bytes (256 RGB triplets) from gfx/palette.lmp; nil uses index-as-grey fallback.
func Init(m *bsp.Map, vertSrc, fragSrc, computeSrc string, palette []byte) (*Renderer, error) {
	prog, err := compileRender(vertSrc, fragSrc)
	if err != nil {
		return nil, fmt.Errorf("compile render shaders: %w", err)
	}

	r := &Renderer{
		worldProg: prog,
		numFaces:  uint32(len(m.Faces)),
		usePVS:    true,
	}
	r.mvpLoc = gl.GetUniformLocation(prog, gl.Str("uMVP\x00"))
	r.usePVSLoc = gl.GetUniformLocation(prog, gl.Str("uUsePVS\x00"))
	r.totalFaceLoc = gl.GetUniformLocation(prog, gl.Str("uTotalFaces\x00"))
	r.atlasLoc = gl.GetUniformLocation(prog, gl.Str("uAtlas\x00"))
	r.atlasSizeLoc = gl.GetUniformLocation(prog, gl.Str("uAtlasSize\x00"))

	// Build texture atlas
	atlasPixels, aw, ah, rects := buildAtlas(m.MipTexes, palette)
	r.atlasW, r.atlasH = int32(aw), int32(ah)

	gl.GenTextures(1, &r.atlasTexture)
	gl.BindTexture(gl.TEXTURE_2D, r.atlasTexture)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGB8, r.atlasW, r.atlasH, 0, gl.RGB, gl.UNSIGNED_BYTE, unsafe.Pointer(&atlasPixels[0]))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	// Per-face atlas info SSBO (binding 5): vec4{atlasX, atlasY, texW, texH} in pixels
	r.ssboFaceAtlas = buildFaceAtlasSSBO(m, rects)

	// Build vertex buffer from BSP faces (6 floats per vertex: x,y,z, faceIdx, s, t)
	verts := buildWorldVerts(m)
	r.numVerts = int32(len(verts) / 6)

	gl.GenVertexArrays(1, &r.vao)
	gl.BindVertexArray(r.vao)

	gl.GenBuffers(1, &r.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, unsafe.Pointer(&verts[0]), gl.STATIC_DRAW)

	// aPos (location 0): 3 floats at offset 0, stride 24
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 24, 0)
	// aFaceIndex (location 1): 1 float at offset 12
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 24, 12)
	// aTexST (location 2): 2 floats at offset 16
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, 24, 16)

	gl.BindVertexArray(0)

	// Compute shader
	cs, err := InitCompute(m, computeSrc)
	if err != nil {
		return nil, err
	}
	r.compute = cs

	// Brightness SSBO (binding 4)
	brightness := bsp.FaceBrightness(m)
	r.ssboBrightness = makeBrightnessSSBO(brightness)

	// Depth test + face cull
	gl.Enable(gl.DEPTH_TEST)
	gl.Enable(gl.CULL_FACE)
	gl.CullFace(gl.FRONT) // Quake front = clockwise

	return r, nil
}

// Draw renders one frame given the player state.
func (r *Renderer) Draw(frame game.RenderFrame, width, height int) {
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.ClearColor(0.1, 0.1, 0.15, 1.0)

	if r.usePVS {
		r.compute.Dispatch(frame.Player.LeafIndex)
	}

	proj := mgl32.Perspective(mgl32.DegToRad(90), float32(width)/float32(height), 4, 16384)

	pos := frame.Player.Position
	yaw := mgl32.DegToRad(frame.Player.Yaw)
	pitch := mgl32.DegToRad(frame.Player.Pitch)

	// Forward vector from yaw+pitch
	forward := mgl32.Vec3{
		float32(math.Cos(float64(pitch)) * math.Cos(float64(yaw))),
		float32(math.Cos(float64(pitch)) * math.Sin(float64(yaw))),
		float32(math.Sin(float64(pitch))),
	}
	target := pos.Add(forward)
	up := mgl32.Vec3{0, 0, 1}
	view := mgl32.LookAtV(pos, target, up)
	mvp := proj.Mul4(view)

	gl.UseProgram(r.worldProg)
	gl.UniformMatrix4fv(r.mvpLoc, 1, false, &mvp[0])
	gl.Uniform1i(r.usePVSLoc, boolToInt32(r.usePVS))
	gl.Uniform1ui(r.totalFaceLoc, r.numFaces)

	r.compute.BindVisFlags()
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, r.ssboBrightness)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, r.ssboFaceAtlas)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.atlasTexture)
	gl.Uniform1i(r.atlasLoc, 0)
	gl.Uniform2f(r.atlasSizeLoc, float32(r.atlasW), float32(r.atlasH))

	gl.BindVertexArray(r.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, r.numVerts)
	gl.BindVertexArray(0)
}

// CountVisible returns visible face count (debug).
func (r *Renderer) CountVisible() int {
	return r.compute.CountVisible()
}

// NumFaces returns total face count.
func (r *Renderer) NumFaces() int { return int(r.numFaces) }

func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// buildWorldVerts triangulates world model (Models[0]) BSP faces into a flat float32 slice.
// Sub-models (Models[1..N]) are brush entities (doors, platforms) and are excluded.
// Each vertex: x, y, z, faceIndex, s, t (6 floats; s/t are raw pixel-space texture coords).
func buildWorldVerts(m *bsp.Map) []float32 {
	world := m.Models[0]
	firstFace := int(world.FirstFace)
	lastFace := firstFace + int(world.NumFaces)

	var verts []float32
	for faceIdx := firstFace; faceIdx < lastFace; faceIdx++ {
		face := m.Faces[faceIdx]
		fi := float32(faceIdx)

		// Texture projection vectors for this face
		var sv, tv [4]float32
		if int(face.TexInfo) < len(m.TexInfos) {
			ti := m.TexInfos[face.TexInfo]
			sv = ti.Vecs[0]
			tv = ti.Vecs[1]
		}

		// Collect face vertices via surfedges
		faceVs := make([][3]float32, 0, face.NumEdges)
		for i := 0; i < int(face.NumEdges); i++ {
			seIdx := int(face.FirstEdge) + i
			se := m.SurfEdges[seIdx]
			var v [3]float32
			if se >= 0 {
				v = m.Vertices[m.Edges[se].V[0]]
			} else {
				v = m.Vertices[m.Edges[-se].V[1]]
			}
			faceVs = append(faceVs, v)
		}
		// Fan triangulation from vertex 0
		for i := 1; i+1 < len(faceVs); i++ {
			for _, vtx := range [][3]float32{faceVs[0], faceVs[i], faceVs[i+1]} {
				s := vtx[0]*sv[0] + vtx[1]*sv[1] + vtx[2]*sv[2] + sv[3]
				t := vtx[0]*tv[0] + vtx[1]*tv[1] + vtx[2]*tv[2] + tv[3]
				verts = append(verts, vtx[0], vtx[1], vtx[2], fi, s, t)
			}
		}
	}
	return verts
}

// buildAtlas packs all miptex greyscale pixels into a single 2D atlas texture.
// Returns pixel data (R8), atlas dimensions, and per-miptex rect {x,y,w,h}.
func buildAtlas(mipTexes []bsp.MipTex, palette []byte) (pixels []byte, atlasW, atlasH int, rects [][4]int32) {
	atlasW = 2048

	// Build RGB lookup from palette; fall back to greyscale ramp if no palette.
	palRGB := make([][3]byte, 256)
	if len(palette) >= 768 {
		for i := 0; i < 256; i++ {
			palRGB[i] = [3]byte{palette[i*3], palette[i*3+1], palette[i*3+2]}
		}
	} else {
		for i := range palRGB {
			v := byte(i)
			palRGB[i] = [3]byte{v, v, v}
		}
	}

	rects = make([][4]int32, len(mipTexes))
	curX, curY, rowH := 0, 0, 0
	for i, mt := range mipTexes {
		if mt.Width == 0 || mt.Height == 0 {
			continue
		}
		if curX+mt.Width > atlasW {
			curY += rowH
			curX = 0
			rowH = 0
		}
		if mt.Height > rowH {
			rowH = mt.Height
		}
		rects[i] = [4]int32{int32(curX), int32(curY), int32(mt.Width), int32(mt.Height)}
		curX += mt.Width
	}
	rawH := curY + rowH
	// Round up to next power of 2
	atlasH = 1
	for atlasH < rawH {
		atlasH <<= 1
	}
	if atlasH == 0 {
		atlasH = 1
	}

	pixels = make([]byte, atlasW*atlasH*3)
	for i, mt := range mipTexes {
		if mt.Width == 0 || mt.Height == 0 || mt.Pixels == nil {
			continue
		}
		rx, ry := int(rects[i][0]), int(rects[i][1])
		for py := 0; py < mt.Height; py++ {
			for px := 0; px < mt.Width; px++ {
				rgb := palRGB[mt.Pixels[py*mt.Width+px]]
				dst := ((ry+py)*atlasW + (rx + px)) * 3
				pixels[dst+0] = rgb[0]
				pixels[dst+1] = rgb[1]
				pixels[dst+2] = rgb[2]
			}
		}
	}
	return
}

// buildFaceAtlasSSBO creates an SSBO (binding 5) with per-face atlas info: vec4{x,y,w,h} in pixels.
func buildFaceAtlasSSBO(m *bsp.Map, rects [][4]int32) uint32 {
	data := make([]float32, len(m.Faces)*4)
	for faceIdx := range m.Faces {
		face := m.Faces[faceIdx]
		if int(face.TexInfo) >= len(m.TexInfos) {
			continue
		}
		mipTexIdx := int(m.TexInfos[face.TexInfo].MipTex)
		if mipTexIdx < 0 || mipTexIdx >= len(rects) {
			continue
		}
		r := rects[mipTexIdx]
		base := faceIdx * 4
		data[base+0] = float32(r[0])
		data[base+1] = float32(r[1])
		data[base+2] = float32(r[2])
		data[base+3] = float32(r[3])
	}
	var ssbo uint32
	gl.GenBuffers(1, &ssbo)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, ssbo)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(data)*4, unsafe.Pointer(&data[0]), gl.STATIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return ssbo
}

// compileRender links vertex + fragment shaders.
func compileRender(vertSrc, fragSrc string) (uint32, error) {
	vert, err := compileShader(vertSrc, gl.VERTEX_SHADER)
	if err != nil {
		return 0, fmt.Errorf("vert: %w", err)
	}
	frag, err := compileShader(fragSrc, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, fmt.Errorf("frag: %w", err)
	}
	return linkProgram(vert, frag)
}

// compileCompute links a compute shader program.
func compileCompute(src string) (uint32, error) {
	shader, err := compileShader(src, gl.COMPUTE_SHADER)
	if err != nil {
		return 0, err
	}
	return linkProgram(shader)
}

func compileShader(src string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)
	csrc, free := gl.Strs(src + "\x00")
	gl.ShaderSource(shader, 1, csrc, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLen int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLen)
		log := strings.Repeat("\x00", int(logLen+1))
		gl.GetShaderInfoLog(shader, logLen, nil, gl.Str(log))
		return 0, fmt.Errorf("shader compile: %s", log)
	}
	return shader, nil
}

func makeBrightnessSSBO(brightness []float32) uint32 {
	var ssbo uint32
	gl.GenBuffers(1, &ssbo)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, ssbo)
	gl.BufferData(gl.SHADER_STORAGE_BUFFER, len(brightness)*4, unsafe.Pointer(&brightness[0]), gl.STATIC_DRAW)
	gl.BindBuffer(gl.SHADER_STORAGE_BUFFER, 0)
	return ssbo
}

func linkProgram(shaders ...uint32) (uint32, error) {
	prog := gl.CreateProgram()
	for _, s := range shaders {
		gl.AttachShader(prog, s)
	}
	gl.LinkProgram(prog)

	var status int32
	gl.GetProgramiv(prog, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLen int32
		gl.GetProgramiv(prog, gl.INFO_LOG_LENGTH, &logLen)
		log := strings.Repeat("\x00", int(logLen+1))
		gl.GetProgramInfoLog(prog, logLen, nil, gl.Str(log))
		return 0, fmt.Errorf("program link: %s", log)
	}
	for _, s := range shaders {
		gl.DeleteShader(s)
	}
	return prog, nil
}
