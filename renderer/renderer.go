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

// FaceVerts holds the triangulated vertex data for one face.
type FaceVerts struct {
	Verts     []float32 // x,y,z, faceIndex (4 floats per vertex)
	FaceIndex uint32
}

// Renderer owns all GL state and draws the world.
type Renderer struct {
	worldProg   uint32
	vao         uint32
	vbo         uint32
	numVerts    int32

	compute        *ComputeState
	usePVS         bool
	numFaces       uint32
	ssboBrightness uint32

	mvpLoc       int32
	usePVSLoc    int32
	totalFaceLoc int32
}

// Init initialises GL state and uploads BSP geometry.
func Init(m *bsp.Map, vertSrc, fragSrc, computeSrc string) (*Renderer, error) {
	prog, err := compileRender(vertSrc, fragSrc)
	if err != nil {
		return nil, fmt.Errorf("compile render shaders: %w", err)
	}

	r := &Renderer{
		worldProg:    prog,
		numFaces:     uint32(len(m.Faces)),
		usePVS:       true,
	}
	r.mvpLoc = gl.GetUniformLocation(prog, gl.Str("uMVP\x00"))
	r.usePVSLoc = gl.GetUniformLocation(prog, gl.Str("uUsePVS\x00"))
	r.totalFaceLoc = gl.GetUniformLocation(prog, gl.Str("uTotalFaces\x00"))

	// Build vertex buffer from BSP faces
	verts := buildWorldVerts(m)
	r.numVerts = int32(len(verts) / 4) // 4 floats per vertex

	gl.GenVertexArrays(1, &r.vao)
	gl.BindVertexArray(r.vao)

	gl.GenBuffers(1, &r.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, unsafe.Pointer(&verts[0]), gl.STATIC_DRAW)

	// aPos
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 16, 0)
	// aFaceIndex
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 16, 12)

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
		r.compute.Dispatch(uint32(frame.Player.LeafIndex))
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
// Each vertex: x, y, z, faceIndex (as float32).
func buildWorldVerts(m *bsp.Map) []float32 {
	world := m.Models[0]
	firstFace := int(world.FirstFace)
	lastFace := firstFace + int(world.NumFaces)

	var verts []float32
	for faceIdx := firstFace; faceIdx < lastFace; faceIdx++ {
		face := m.Faces[faceIdx]
		fi := float32(faceIdx)
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
				verts = append(verts, vtx[0], vtx[1], vtx[2], fi)
			}
		}
	}
	return verts
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
