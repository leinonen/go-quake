package renderer

import (
	"fmt"
	"strings"
	"time"
	"unsafe"

	"math"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/game"
)

// entityRenderable holds the VAO for one brush entity sub-model.
type entityRenderable struct {
	vao      uint32
	numVerts int32
}

// itemGroup holds one VAO+texture pair for a single texture within an item model frame.
type itemGroup struct {
	vao      uint32
	numVerts int32
	tex      uint32
}

// itemRenderable holds all frames for one unique item model.
// Each frame is a slice of texture groups (MDL = 1 group, BSP = N groups).
type itemRenderable struct {
	frames [][]itemGroup // [frameIdx][groupIdx]
}

// weaponFrameData holds one VAO for one weapon animation frame.
type weaponFrameData struct {
	vao      uint32
	numVerts int32
}

// WeaponMesh contains view-weapon geometry and skin data ready for upload.
type WeaponMesh struct {
	Verts  []float32 // interleaved x,y,z,u,v (5 floats per vertex, 3 verts per triangle)
	TexRGB []byte    // packed RGB pixels
	TexW   int
	TexH   int
}

// ItemModel holds all animation frames for one unique item or monster model.
type ItemModel struct {
	Frames [][]*WeaponMesh // [frameIdx] → texture groups
}

// Renderer owns all GL state and draws the world.
type Renderer struct {
	worldProg uint32
	vao       uint32
	vbo       uint32
	numVerts  int32

	compute          *ComputeState
	usePVS           bool
	numFaces         uint32
	ssboBrightness   uint32
	ssboFaceAtlas    uint32
	atlasTexture     uint32
	atlasW, atlasH   int32

	mvpLoc          int32
	usePVSLoc       int32
	totalFaceLoc    int32
	atlasLoc        int32
	atlasSizeLoc    int32
	entityOffsetLoc int32
	timeLoc         int32

	skyProg    uint32
	skyVAO     uint32
	skyMVPLoc  int32
	skyTimeLoc int32

	// view weapon — one VAO per animation frame, single shared texture
	weaponProg   uint32
	weaponFrames []weaponFrameData
	weaponTex    uint32
	weapProjLoc  int32
	weapMatLoc   int32
	weapTexLoc   int32

	// HUD health bar
	hudProg    uint32
	hudVAO     uint32
	hudFracLoc int32

	startTime  time.Time
	entityVAOs []entityRenderable
	itemVAOs   []itemRenderable
}

// skyboxVerts is a unit cube (36 vertices, Z-up) used as the skybox mesh.
var skyboxVerts = [...]float32{
	// Bottom (Z = -1)
	-1, -1, -1, 1, -1, -1, 1, 1, -1,
	1, 1, -1, -1, 1, -1, -1, -1, -1,
	// Top (Z = +1)
	-1, -1, 1, -1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, -1, 1, -1, -1, 1,
	// Front (Y = -1)
	-1, -1, -1, -1, -1, 1, 1, -1, 1,
	1, -1, 1, 1, -1, -1, -1, -1, -1,
	// Back (Y = +1)
	1, 1, -1, 1, 1, 1, -1, 1, 1,
	-1, 1, 1, -1, 1, -1, 1, 1, -1,
	// Left (X = -1)
	-1, 1, -1, -1, 1, 1, -1, -1, 1,
	-1, -1, 1, -1, -1, -1, -1, 1, -1,
	// Right (X = +1)
	1, -1, -1, 1, -1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, -1, 1, -1, -1,
}

// Init initialises GL state and uploads BSP geometry.
// palette is 768 bytes (256 RGB triplets) from gfx/palette.lmp; nil uses index-as-grey fallback.
// weapon is a slice of per-frame WeaponMesh (one per animation frame); nil/empty skips weapon rendering.
// items is a slice of per-unique-model ItemModel for world item pickups and monsters; may be nil.
func Init(m *bsp.Map,
	vertSrc, fragSrc, computeSrc,
	skyVertSrc, skyFragSrc,
	weapVertSrc, weapFragSrc,
	hudVertSrc, hudFragSrc string,
	palette []byte,
	weapon []*WeaponMesh,
	items []ItemModel) (*Renderer, error) {

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
	r.entityOffsetLoc = gl.GetUniformLocation(prog, gl.Str("uEntityOffset\x00"))
	r.timeLoc = gl.GetUniformLocation(prog, gl.Str("uTime\x00"))
	r.startTime = time.Now()

	// Skybox shader
	skyProg, err := compileRender(skyVertSrc, skyFragSrc)
	if err != nil {
		return nil, fmt.Errorf("compile skybox shaders: %w", err)
	}
	r.skyProg = skyProg
	r.skyMVPLoc = gl.GetUniformLocation(skyProg, gl.Str("uMVP\x00"))
	r.skyTimeLoc = gl.GetUniformLocation(skyProg, gl.Str("uTime\x00"))

	// Skybox cube VAO
	gl.GenVertexArrays(1, &r.skyVAO)
	gl.BindVertexArray(r.skyVAO)
	var skyVBO uint32
	gl.GenBuffers(1, &skyVBO)
	gl.BindBuffer(gl.ARRAY_BUFFER, skyVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(skyboxVerts)*4, unsafe.Pointer(&skyboxVerts[0]), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 12, 0)
	gl.BindVertexArray(0)

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

	// Per-face atlas info SSBO (binding 5)
	r.ssboFaceAtlas = buildFaceAtlasSSBO(m, rects)

	// Build vertex buffer from BSP faces (6 floats per vertex: x,y,z, faceIdx, s, t)
	verts := buildModelVerts(m, 0)
	r.numVerts = int32(len(verts) / 6)

	gl.GenVertexArrays(1, &r.vao)
	gl.BindVertexArray(r.vao)
	gl.GenBuffers(1, &r.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, unsafe.Pointer(&verts[0]), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 24, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 24, 12)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, 24, 16)
	gl.BindVertexArray(0)

	// Build VAOs for brush entity sub-models (Models[1..N])
	for i := 1; i < len(m.Models); i++ {
		ev := buildModelVerts(m, i)
		if len(ev) == 0 {
			r.entityVAOs = append(r.entityVAOs, entityRenderable{})
			continue
		}
		var evao, evbo uint32
		gl.GenVertexArrays(1, &evao)
		gl.BindVertexArray(evao)
		gl.GenBuffers(1, &evbo)
		gl.BindBuffer(gl.ARRAY_BUFFER, evbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(ev)*4, unsafe.Pointer(&ev[0]), gl.STATIC_DRAW)
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 24, 0)
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 24, 12)
		gl.EnableVertexAttribArray(2)
		gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, 24, 16)
		gl.BindVertexArray(0)
		r.entityVAOs = append(r.entityVAOs, entityRenderable{vao: evao, numVerts: int32(len(ev) / 6)})
	}

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

	// Weapon/item shader — compile if either weapon frames or items are present
	if len(weapon) > 0 || len(items) > 0 {
		wp, err := compileRender(weapVertSrc, weapFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile weapon shaders: %w", err)
		}
		r.weaponProg = wp
		r.weapProjLoc = gl.GetUniformLocation(wp, gl.Str("uProj\x00"))
		r.weapMatLoc = gl.GetUniformLocation(wp, gl.Str("uWeaponMat\x00"))
		r.weapTexLoc = gl.GetUniformLocation(wp, gl.Str("uTex\x00"))
	}

	// View weapon — upload one VAO per animation frame, texture once from frame 0
	if len(weapon) > 0 {
		// Upload skin texture from first valid frame
		for _, wf := range weapon {
			if wf != nil && len(wf.TexRGB) > 0 {
				gl.GenTextures(1, &r.weaponTex)
				gl.BindTexture(gl.TEXTURE_2D, r.weaponTex)
				gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGB8, int32(wf.TexW), int32(wf.TexH), 0,
					gl.RGB, gl.UNSIGNED_BYTE, unsafe.Pointer(&wf.TexRGB[0]))
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
				gl.BindTexture(gl.TEXTURE_2D, 0)
				break
			}
		}
		// Upload one VAO per frame
		for _, wf := range weapon {
			if wf == nil || len(wf.Verts) == 0 {
				r.weaponFrames = append(r.weaponFrames, weaponFrameData{})
				continue
			}
			var wvao, wvbo uint32
			gl.GenVertexArrays(1, &wvao)
			gl.BindVertexArray(wvao)
			gl.GenBuffers(1, &wvbo)
			gl.BindBuffer(gl.ARRAY_BUFFER, wvbo)
			gl.BufferData(gl.ARRAY_BUFFER, len(wf.Verts)*4, unsafe.Pointer(&wf.Verts[0]), gl.STATIC_DRAW)
			gl.EnableVertexAttribArray(0)
			gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 20, 0)
			gl.EnableVertexAttribArray(1)
			gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, 20, 12)
			gl.BindVertexArray(0)
			r.weaponFrames = append(r.weaponFrames, weaponFrameData{
				vao:      wvao,
				numVerts: int32(len(wf.Verts) / 5),
			})
		}
	}

	// Upload item model VAOs — each ItemModel has frames, each frame has texture groups.
	for _, im := range items {
		var ir itemRenderable
		for _, frameGroups := range im.Frames {
			var groups []itemGroup
			for _, g := range frameGroups {
				if g == nil || len(g.Verts) == 0 || len(g.TexRGB) == 0 {
					continue
				}
				var ivao, ivbo uint32
				gl.GenVertexArrays(1, &ivao)
				gl.BindVertexArray(ivao)
				gl.GenBuffers(1, &ivbo)
				gl.BindBuffer(gl.ARRAY_BUFFER, ivbo)
				gl.BufferData(gl.ARRAY_BUFFER, len(g.Verts)*4, unsafe.Pointer(&g.Verts[0]), gl.STATIC_DRAW)
				gl.EnableVertexAttribArray(0)
				gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 20, 0)
				gl.EnableVertexAttribArray(1)
				gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, 20, 12)
				gl.BindVertexArray(0)

				var itex uint32
				gl.GenTextures(1, &itex)
				gl.BindTexture(gl.TEXTURE_2D, itex)
				gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGB8, int32(g.TexW), int32(g.TexH), 0,
					gl.RGB, gl.UNSIGNED_BYTE, unsafe.Pointer(&g.TexRGB[0]))
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.REPEAT)
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.REPEAT)
				gl.BindTexture(gl.TEXTURE_2D, 0)

				groups = append(groups, itemGroup{
					vao:      ivao,
					numVerts: int32(len(g.Verts) / 5),
					tex:      itex,
				})
			}
			ir.frames = append(ir.frames, groups)
		}
		r.itemVAOs = append(r.itemVAOs, ir)
	}

	// HUD health bar — compile shader and upload static quad VAO
	if len(hudVertSrc) > 0 && len(hudFragSrc) > 0 {
		hp, err := compileRender(hudVertSrc, hudFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile HUD shaders: %w", err)
		}
		r.hudProg = hp
		r.hudFracLoc = gl.GetUniformLocation(hp, gl.Str("uFrac\x00"))

		// NDC quad covering bottom strip: x[-1,1], y[-1,-0.97], uv.x[0,1]
		// Format: x, y, u, v (4 floats per vertex)
		hudVerts := [...]float32{
			-1, -1, 0, 0,
			1, -1, 1, 0,
			1, -0.97, 1, 1,
			-1, -1, 0, 0,
			1, -0.97, 1, 1,
			-1, -0.97, 0, 1,
		}
		gl.GenVertexArrays(1, &r.hudVAO)
		gl.BindVertexArray(r.hudVAO)
		var hvbo uint32
		gl.GenBuffers(1, &hvbo)
		gl.BindBuffer(gl.ARRAY_BUFFER, hvbo)
		gl.BufferData(gl.ARRAY_BUFFER, len(hudVerts)*4, unsafe.Pointer(&hudVerts[0]), gl.STATIC_DRAW)
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 16, 0)
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, 16, 8)
		gl.BindVertexArray(0)
	}

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
	gl.Uniform1f(r.timeLoc, float32(time.Since(r.startTime).Seconds()))

	r.compute.BindVisFlags()
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 4, r.ssboBrightness)
	gl.BindBufferBase(gl.SHADER_STORAGE_BUFFER, 5, r.ssboFaceAtlas)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, r.atlasTexture)
	gl.Uniform1i(r.atlasLoc, 0)
	gl.Uniform2f(r.atlasSizeLoc, float32(r.atlasW), float32(r.atlasH))

	// Draw world (PVS on, no entity offset)
	gl.Uniform3f(r.entityOffsetLoc, 0, 0, 0)
	gl.BindVertexArray(r.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, r.numVerts)
	gl.BindVertexArray(0)

	// Draw brush entities (PVS off, per-entity offset)
	if len(frame.Player.Entities) > 0 {
		gl.Uniform1i(r.usePVSLoc, 0)
		for _, es := range frame.Player.Entities {
			idx := es.ModelIndex - 1
			if idx < 0 || idx >= len(r.entityVAOs) {
				continue
			}
			er := r.entityVAOs[idx]
			if er.vao == 0 || er.numVerts == 0 {
				continue
			}
			gl.Uniform3f(r.entityOffsetLoc, es.Offset[0], es.Offset[1], es.Offset[2])
			gl.BindVertexArray(er.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, er.numVerts)
			gl.BindVertexArray(0)
		}
		gl.Uniform1i(r.usePVSLoc, boolToInt32(r.usePVS))
	}

	// Draw world items and monsters — MDL/BSP models at their origins.
	if r.weaponProg != 0 && len(frame.Items) > 0 {
		gl.Disable(gl.CULL_FACE)
		gl.UseProgram(r.weaponProg)
		gl.UniformMatrix4fv(r.weapProjLoc, 1, false, &proj[0])
		gl.Uniform1i(r.weapTexLoc, 0)
		gl.ActiveTexture(gl.TEXTURE0)
		for _, is := range frame.Items {
			if is.MdlIdx < 0 || is.MdlIdx >= len(r.itemVAOs) {
				continue
			}
			ir := r.itemVAOs[is.MdlIdx]
			if len(ir.frames) == 0 {
				continue
			}
			frameIdx := is.Frame
			if frameIdx < 0 || frameIdx >= len(ir.frames) {
				frameIdx = 0
			}
			itemMat := view.Mul4(mgl32.Translate3D(is.Pos[0], is.Pos[1], is.Pos[2]).Mul4(
				mgl32.HomogRotate3DZ(is.Yaw)))
			gl.UniformMatrix4fv(r.weapMatLoc, 1, false, &itemMat[0])
			for _, g := range ir.frames[frameIdx] {
				gl.BindTexture(gl.TEXTURE_2D, g.tex)
				gl.BindVertexArray(g.vao)
				gl.DrawArrays(gl.TRIANGLES, 0, g.numVerts)
			}
		}
		gl.BindVertexArray(0)
		gl.BindTexture(gl.TEXTURE_2D, 0)
		gl.Enable(gl.CULL_FACE)
	}

	// Draw skybox last — rotation-only view so it's infinitely far away.
	skyView := mgl32.LookAtV(mgl32.Vec3{0, 0, 0}, forward, up)
	skyMVP := proj.Mul4(skyView)
	gl.DepthFunc(gl.LEQUAL)
	gl.Disable(gl.CULL_FACE)
	gl.UseProgram(r.skyProg)
	gl.UniformMatrix4fv(r.skyMVPLoc, 1, false, &skyMVP[0])
	gl.Uniform1f(r.skyTimeLoc, float32(time.Since(r.startTime).Seconds()))
	gl.BindVertexArray(r.skyVAO)
	gl.DrawArrays(gl.TRIANGLES, 0, 36)
	gl.BindVertexArray(0)
	gl.Enable(gl.CULL_FACE)
	gl.DepthFunc(gl.LESS)

	// Draw view weapon on top of everything — clear depth so it is never
	// occluded by world geometry.
	if r.weaponProg != 0 && len(r.weaponFrames) > 0 {
		gl.Clear(gl.DEPTH_BUFFER_BIT)
		gl.Disable(gl.CULL_FACE)

		// Select frame, clamped to valid range
		wfIdx := frame.Player.WeaponFrame
		if wfIdx < 0 || wfIdx >= len(r.weaponFrames) {
			wfIdx = 0
		}
		wf := r.weaponFrames[wfIdx]
		if wf.vao != 0 {
			rotZ := mgl32.HomogRotate3DZ(mgl32.DegToRad(90))
			rotX := mgl32.HomogRotate3DX(mgl32.DegToRad(-90))
			rot := rotX.Mul4(rotZ)
			trans := mgl32.Translate3D(16, -10, -25)
			weaponMat := trans.Mul4(rot)

			gl.UseProgram(r.weaponProg)
			gl.UniformMatrix4fv(r.weapProjLoc, 1, false, &proj[0])
			gl.UniformMatrix4fv(r.weapMatLoc, 1, false, &weaponMat[0])

			gl.ActiveTexture(gl.TEXTURE0)
			gl.BindTexture(gl.TEXTURE_2D, r.weaponTex)
			gl.Uniform1i(r.weapTexLoc, 0)

			gl.BindVertexArray(wf.vao)
			gl.DrawArrays(gl.TRIANGLES, 0, wf.numVerts)
			gl.BindVertexArray(0)
		}

		gl.Enable(gl.CULL_FACE)
	}

	// Draw HUD health bar — last, depth test disabled.
	if r.hudProg != 0 {
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.CULL_FACE)
		gl.UseProgram(r.hudProg)
		frac := float32(frame.Player.Health) / 100.0
		if frac < 0 {
			frac = 0
		} else if frac > 1 {
			frac = 1
		}
		gl.Uniform1f(r.hudFracLoc, frac)
		gl.BindVertexArray(r.hudVAO)
		gl.DrawArrays(gl.TRIANGLES, 0, 6)
		gl.BindVertexArray(0)
		gl.Enable(gl.CULL_FACE)
		gl.Enable(gl.DEPTH_TEST)
	}
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

// buildModelVerts triangulates BSP faces for the given model index into a flat float32 slice.
// Each vertex: x, y, z, faceIndex, s, t (6 floats; s/t are raw pixel-space texture coords).
func buildModelVerts(m *bsp.Map, modelIdx int) []float32 {
	if modelIdx < 0 || modelIdx >= len(m.Models) {
		return nil
	}
	world := m.Models[modelIdx]
	firstFace := int(world.FirstFace)
	lastFace := firstFace + int(world.NumFaces)

	var verts []float32
	for faceIdx := firstFace; faceIdx < lastFace; faceIdx++ {
		face := m.Faces[faceIdx]
		fi := float32(faceIdx)

		var sv, tv [4]float32
		if int(face.TexInfo) < len(m.TexInfos) {
			ti := m.TexInfos[face.TexInfo]
			sv = ti.Vecs[0]
			tv = ti.Vecs[1]
		}

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

// buildAtlas packs all miptex pixels into a single 2D atlas texture.
func buildAtlas(mipTexes []bsp.MipTex, palette []byte) (pixels []byte, atlasW, atlasH int, rects [][4]int32) {
	atlasW = 2048

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

// BuildBSPItemMesh converts a small BSP sub-model file into per-texture WeaponMesh groups.
func BuildBSPItemMesh(data []byte, palette []byte) ([]*WeaponMesh, error) {
	m, err := bsp.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if len(m.Models) == 0 {
		return nil, fmt.Errorf("no models in item BSP")
	}

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

	model := m.Models[0]
	firstFace := int(model.FirstFace)
	lastFace := firstFace + int(model.NumFaces)

	texVerts := map[int][]float32{}
	for faceIdx := firstFace; faceIdx < lastFace; faceIdx++ {
		face := m.Faces[faceIdx]
		if int(face.TexInfo) >= len(m.TexInfos) {
			continue
		}
		ti := m.TexInfos[face.TexInfo]
		mipIdx := int(ti.MipTex)
		if mipIdx < 0 || mipIdx >= len(m.MipTexes) {
			continue
		}
		mt := m.MipTexes[mipIdx]
		if mt.Width == 0 || mt.Height == 0 || mt.Pixels == nil {
			continue
		}
		sv, tv := ti.Vecs[0], ti.Vecs[1]

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
		for i := 1; i+1 < len(faceVs); i++ {
			for _, vtx := range [][3]float32{faceVs[0], faceVs[i], faceVs[i+1]} {
				s := vtx[0]*sv[0] + vtx[1]*sv[1] + vtx[2]*sv[2] + sv[3]
				t := vtx[0]*tv[0] + vtx[1]*tv[1] + vtx[2]*tv[2] + tv[3]
				u := s / float32(mt.Width)
				v2 := t / float32(mt.Height)
				texVerts[mipIdx] = append(texVerts[mipIdx], vtx[0], vtx[1], vtx[2], u, v2)
			}
		}
	}

	if len(texVerts) == 0 {
		return nil, fmt.Errorf("no geometry in item BSP")
	}

	var groups []*WeaponMesh
	for mipIdx, verts := range texVerts {
		mt := m.MipTexes[mipIdx]
		texRGB := make([]byte, len(mt.Pixels)*3)
		for i, idx := range mt.Pixels {
			rgb := palRGB[idx]
			texRGB[i*3+0] = rgb[0]
			texRGB[i*3+1] = rgb[1]
			texRGB[i*3+2] = rgb[2]
		}
		groups = append(groups, &WeaponMesh{
			Verts:  verts,
			TexRGB: texRGB,
			TexW:   mt.Width,
			TexH:   mt.Height,
		})
	}
	return groups, nil
}

// buildFaceAtlasSSBO creates an SSBO (binding 5) with per-face atlas info.
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
