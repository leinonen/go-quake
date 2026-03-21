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
	"go-quake/gfx"
	"go-quake/physics"
)

// HUDAssets holds decoded LMP sprites for the in-game status bar.
// All fields are optional; nil means that element won't be drawn.
type HUDAssets struct {
	SBar       *gfx.LMPImage    // status bar background (320×24)
	Nums       [10]*gfx.LMPImage // big digit sprites 0–9
	Faces      [5]*gfx.LMPImage  // face sprites for health ranges (index 0 = high health)
	WeaponsDim [8]*gfx.LMPImage  // inv_  series: dim/unowned weapon icons
	WeaponsLit [8]*gfx.LMPImage  // inv2_ series: lit/owned weapon icons
	SmallNums  [10]*gfx.LMPImage // 8×8 ammo digit sprites
}

// hudSprite stores normalised atlas UV coordinates and pixel size for one sprite.
type hudSprite struct{ u0, v0, u1, v1, pw, ph float32 }

// hudGradFragSrc is the fallback gradient bar shader.
// uKind: 0=health (green→red gradient), 1=armor (gold), 2=ammo (blue)
const hudGradFragSrc = `#version 430 core
uniform float uFrac;
uniform int   uKind;
in vec2 vUV;
out vec4 FragColor;
void main() {
    if (vUV.x > uFrac) discard;
    if (uKind == 1) {
        FragColor = vec4(0.9, 0.7, 0.1, 1.0);
    } else if (uKind == 2) {
        FragColor = vec4(0.2, 0.5, 1.0, 1.0);
    } else {
        float r = 1.0 - uFrac;
        float g = uFrac;
        FragColor = vec4(r, g, 0.1, 1.0);
    }
}
`

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

// WeaponModel holds all animation frames for one view weapon MDL (one mesh per frame, shared texture).
type WeaponModel struct {
	Frames []*WeaponMesh // [frameIdx]
}

// weaponRenderable holds the uploaded GL resources for one view weapon slot.
type weaponRenderable struct {
	frames []weaponFrameData
	tex    uint32
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
	lightmapTexture  uint32

	mvpLoc          int32
	usePVSLoc       int32
	totalFaceLoc    int32
	atlasLoc        int32
	atlasSizeLoc    int32
	entityOffsetLoc int32
	timeLoc         int32
	lightmapLoc     int32

	skyProg    uint32
	skyVAO     uint32
	skyMVPLoc  int32
	skyTimeLoc int32

	// view weapons — one weaponRenderable per slot (8 total), each with per-frame VAOs + texture
	weaponProg  uint32
	weapons     []weaponRenderable
	weapProjLoc int32
	weapMatLoc  int32
	weapTexLoc  int32

	// HUD — gradient fallback (always available)
	hudGradProg uint32
	hudFracLoc  int32
	hudKindLoc  int32

	// HUD — sprite atlas (used when HUDAssets were provided)
	hudProg     uint32
	hudTexLoc   int32
	hudAtlasTex uint32
	hudVAO      uint32
	hudVBO      uint32
	hudSBar      hudSprite
	hudNums      [10]hudSprite
	hudFaces     [5]hudSprite
	hudSBarW     float32 // pixel width of sbar sprite
	hudSBarH     float32 // pixel height of sbar sprite
	hudValid     bool    // true when atlas sprites are ready
	hudWeapDim   [8]hudSprite
	hudWeapLit   [8]hudSprite
	hudSmallNums [10]hudSprite
	hudGoldTexel hudSprite
	hudWeapBarY  float32 // virtual y where weapon bar starts (= sbarH)
	hudTotalH    float32 // total virtual height (sbarH + weapon bar height)

	// underwater tint overlay
	underwaterProg    uint32
	underwaterVAO     uint32
	underwaterTimeLoc int32

	// blood particles and sparks
	particleProg    uint32
	particleVAO     uint32
	particleVBO     uint32
	partMVPLoc      int32
	particleScratch []float32 // reused each frame

	// bullet tracers
	tracerProg    uint32
	tracerVAO     uint32
	tracerVBO     uint32
	tracerMVPLoc  int32
	tracerScratch []float32

	startTime    time.Time
	bobPhase     float32
	lastDrawTime time.Time
	smoothedRoll float32
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
// weapons is a slice of per-slot WeaponModel (8 slots, one per weapon type); nil/empty skips weapon rendering.
// items is a slice of per-unique-model ItemModel for world item pickups and monsters; may be nil.
func Init(m *bsp.Map,
	vertSrc, fragSrc, computeSrc,
	skyVertSrc, skyFragSrc,
	weapVertSrc, weapFragSrc,
	hudVertSrc, hudFragSrc,
	partVertSrc, partFragSrc,
	uwVertSrc, uwFragSrc,
	tracerVertSrc, tracerFragSrc string,
	palette []byte,
	weapons []WeaponModel,
	items []ItemModel,
	hudAssets *HUDAssets) (*Renderer, error) {

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
	r.lightmapLoc = gl.GetUniformLocation(prog, gl.Str("uLightmap\x00"))
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

	// Build lightmap atlas and upload as TEXTURE1
	lmPixels, lmW, lmH, lmInfos := bsp.BuildLightmapAtlas(m)
	gl.GenTextures(1, &r.lightmapTexture)
	gl.BindTexture(gl.TEXTURE_2D, r.lightmapTexture)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGB8, int32(lmW), int32(lmH), 0,
		gl.RGB, gl.UNSIGNED_BYTE, unsafe.Pointer(&lmPixels[0]))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	// Build vertex buffer from BSP faces (8 floats per vertex: x,y,z, faceIdx, s, t, lmU, lmV)
	verts := buildModelVerts(m, 0, lmInfos, lmW, lmH)
	r.numVerts = int32(len(verts) / 8)

	gl.GenVertexArrays(1, &r.vao)
	gl.BindVertexArray(r.vao)
	gl.GenBuffers(1, &r.vbo)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(verts)*4, unsafe.Pointer(&verts[0]), gl.STATIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 32, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 32, 12)
	gl.EnableVertexAttribArray(2)
	gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, 32, 16)
	gl.EnableVertexAttribArray(3)
	gl.VertexAttribPointerWithOffset(3, 2, gl.FLOAT, false, 32, 24)
	gl.BindVertexArray(0)

	// Build VAOs for brush entity sub-models (Models[1..N])
	for i := 1; i < len(m.Models); i++ {
		ev := buildModelVerts(m, i, lmInfos, lmW, lmH)
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
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 32, 0)
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 32, 12)
		gl.EnableVertexAttribArray(2)
		gl.VertexAttribPointerWithOffset(2, 2, gl.FLOAT, false, 32, 16)
		gl.EnableVertexAttribArray(3)
		gl.VertexAttribPointerWithOffset(3, 2, gl.FLOAT, false, 32, 24)
		gl.BindVertexArray(0)
		r.entityVAOs = append(r.entityVAOs, entityRenderable{vao: evao, numVerts: int32(len(ev) / 8)})
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

	// Weapon/item shader — compile if either weapon slots or items are present
	if len(weapons) > 0 || len(items) > 0 {
		wp, err := compileRender(weapVertSrc, weapFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile weapon shaders: %w", err)
		}
		r.weaponProg = wp
		r.weapProjLoc = gl.GetUniformLocation(wp, gl.Str("uProj\x00"))
		r.weapMatLoc = gl.GetUniformLocation(wp, gl.Str("uWeaponMat\x00"))
		r.weapTexLoc = gl.GetUniformLocation(wp, gl.Str("uTex\x00"))
	}

	// View weapons — upload one weaponRenderable per slot.
	for _, wm := range weapons {
		var wr weaponRenderable
		// Upload skin texture from first valid frame.
		for _, wf := range wm.Frames {
			if wf != nil && len(wf.TexRGB) > 0 {
				gl.GenTextures(1, &wr.tex)
				gl.BindTexture(gl.TEXTURE_2D, wr.tex)
				gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGB8, int32(wf.TexW), int32(wf.TexH), 0,
					gl.RGB, gl.UNSIGNED_BYTE, unsafe.Pointer(&wf.TexRGB[0]))
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
				gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
				gl.BindTexture(gl.TEXTURE_2D, 0)
				break
			}
		}
		// Upload one VAO per animation frame.
		for _, wf := range wm.Frames {
			if wf == nil || len(wf.Verts) == 0 {
				wr.frames = append(wr.frames, weaponFrameData{})
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
			wr.frames = append(wr.frames, weaponFrameData{
				vao:      wvao,
				numVerts: int32(len(wf.Verts) / 5),
			})
		}
		r.weapons = append(r.weapons, wr)
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

	// HUD — always compile gradient fallback shader.
	if len(hudVertSrc) > 0 {
		gp, err := compileRender(hudVertSrc, hudGradFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile HUD gradient shader: %w", err)
		}
		r.hudGradProg = gp
		r.hudFracLoc = gl.GetUniformLocation(gp, gl.Str("uFrac\x00"))
		r.hudKindLoc = gl.GetUniformLocation(gp, gl.Str("uKind\x00"))
	}

	// HUD — compile atlas shader and build sprite atlas when assets are provided.
	if hudAssets != nil && len(hudVertSrc) > 0 && len(hudFragSrc) > 0 {
		ap, err := compileRender(hudVertSrc, hudFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile HUD atlas shader: %w", err)
		}
		r.hudProg = ap
		r.hudTexLoc = gl.GetUniformLocation(ap, gl.Str("uHUDTex\x00"))
		r.hudValid = buildHUDAtlas(r, hudAssets)
	}

	// HUD — dynamic VBO (shared by both gradient and atlas paths).
	// Capacity: 128 quads × 6 verts × 4 floats per vert × 4 bytes.
	gl.GenVertexArrays(1, &r.hudVAO)
	gl.BindVertexArray(r.hudVAO)
	gl.GenBuffers(1, &r.hudVBO)
	gl.BindBuffer(gl.ARRAY_BUFFER, r.hudVBO)
	gl.BufferData(gl.ARRAY_BUFFER, 128*6*4*4, nil, gl.DYNAMIC_DRAW)
	gl.EnableVertexAttribArray(0)
	gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 16, 0)
	gl.EnableVertexAttribArray(1)
	gl.VertexAttribPointerWithOffset(1, 2, gl.FLOAT, false, 16, 8)
	gl.BindVertexArray(0)

	// Particle shader + dynamic VBO (2048 particles × 6 floats × 4 bytes)
	if len(partVertSrc) > 0 && len(partFragSrc) > 0 {
		pp, err := compileRender(partVertSrc, partFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile particle shaders: %w", err)
		}
		r.particleProg = pp
		r.partMVPLoc = gl.GetUniformLocation(pp, gl.Str("uMVP\x00"))

		gl.GenVertexArrays(1, &r.particleVAO)
		gl.BindVertexArray(r.particleVAO)
		gl.GenBuffers(1, &r.particleVBO)
		gl.BindBuffer(gl.ARRAY_BUFFER, r.particleVBO)
		gl.BufferData(gl.ARRAY_BUFFER, 2048*6*4, nil, gl.DYNAMIC_DRAW)
		// aPos: 3 floats at offset 0, stride 24
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 24, 0)
		// aLife: 1 float at offset 12
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 24, 12)
		// aStuck: 1 float at offset 16
		gl.EnableVertexAttribArray(2)
		gl.VertexAttribPointerWithOffset(2, 1, gl.FLOAT, false, 24, 16)
		// aKind: 1 float at offset 20
		gl.EnableVertexAttribArray(3)
		gl.VertexAttribPointerWithOffset(3, 1, gl.FLOAT, false, 24, 20)
		gl.BindVertexArray(0)

		gl.Enable(gl.PROGRAM_POINT_SIZE)
		r.particleScratch = make([]float32, 0, 2048*6)
	}

	// Tracer shader + dynamic VBO (128 tracers × 2 vertices × 4 floats × 4 bytes)
	if len(tracerVertSrc) > 0 && len(tracerFragSrc) > 0 {
		tp, err := compileRender(tracerVertSrc, tracerFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile tracer shaders: %w", err)
		}
		r.tracerProg = tp
		r.tracerMVPLoc = gl.GetUniformLocation(tp, gl.Str("uMVP\x00"))

		gl.GenVertexArrays(1, &r.tracerVAO)
		gl.BindVertexArray(r.tracerVAO)
		gl.GenBuffers(1, &r.tracerVBO)
		gl.BindBuffer(gl.ARRAY_BUFFER, r.tracerVBO)
		gl.BufferData(gl.ARRAY_BUFFER, 128*2*4*4, nil, gl.DYNAMIC_DRAW)
		// aPos: 3 floats at offset 0, stride 16
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 3, gl.FLOAT, false, 16, 0)
		// aLife: 1 float at offset 12
		gl.EnableVertexAttribArray(1)
		gl.VertexAttribPointerWithOffset(1, 1, gl.FLOAT, false, 16, 12)
		gl.BindVertexArray(0)

		r.tracerScratch = make([]float32, 0, 128*8)
	}

	// Underwater tint overlay — fullscreen quad shader
	if len(uwVertSrc) > 0 && len(uwFragSrc) > 0 {
		up, err := compileRender(uwVertSrc, uwFragSrc)
		if err != nil {
			return nil, fmt.Errorf("compile underwater shaders: %w", err)
		}
		r.underwaterProg = up
		r.underwaterTimeLoc = gl.GetUniformLocation(up, gl.Str("uTime\x00"))

		// Fullscreen NDC quad: x[-1,1], y[-1,1], 6 vertices, 2 floats each
		uwVerts := [...]float32{
			-1, -1,
			1, -1,
			1, 1,
			-1, -1,
			1, 1,
			-1, 1,
		}
		gl.GenVertexArrays(1, &r.underwaterVAO)
		gl.BindVertexArray(r.underwaterVAO)
		var uwVBO uint32
		gl.GenBuffers(1, &uwVBO)
		gl.BindBuffer(gl.ARRAY_BUFFER, uwVBO)
		gl.BufferData(gl.ARRAY_BUFFER, len(uwVerts)*4, unsafe.Pointer(&uwVerts[0]), gl.STATIC_DRAW)
		gl.EnableVertexAttribArray(0)
		gl.VertexAttribPointerWithOffset(0, 2, gl.FLOAT, false, 8, 0)
		gl.BindVertexArray(0)
	}

	return r, nil
}

// Draw renders one frame given the physics state.
func (r *Renderer) Draw(p *physics.Physics, width, height int) {
	gl.Clear(gl.COLOR_BUFFER_BIT | gl.DEPTH_BUFFER_BIT)
	gl.ClearColor(0.1, 0.1, 0.15, 1.0)

	if r.usePVS {
		r.compute.Dispatch(p.LeafIndex)
	}

	proj := mgl32.Perspective(mgl32.DegToRad(90), float32(width)/float32(height), 4, 16384)

	pos := p.Pos
	yaw := mgl32.DegToRad(p.Yaw)
	pitch := mgl32.DegToRad(p.Pitch)

	// Per-frame dt for bob accumulation
	now := time.Now()
	dt := float32(now.Sub(r.lastDrawTime).Seconds())
	if r.lastDrawTime.IsZero() || dt > 0.1 {
		dt = 0
	}
	r.lastDrawTime = now

	// Horizontal speed (XY only)
	hspeed := float32(math.Sqrt(float64(p.Velocity[0]*p.Velocity[0] + p.Velocity[1]*p.Velocity[1])))

	// Advance bob phase only when moving on ground
	if p.OnGround && hspeed > 10 {
		r.bobPhase += dt * hspeed / 320.0
	}

	// Normalised amplitude 0..1 based on speed
	bobAmp := hspeed / 320.0

	// Bob offsets (vertical at 2× frequency of horizontal)
	bobV := float32(math.Sin(float64(r.bobPhase*math.Pi*2))) * bobAmp * 2.0 // ±2 units vertical
	bobH := float32(math.Sin(float64(r.bobPhase*math.Pi))) * bobAmp * 1.0   // ±1 unit horizontal

	// Forward vector from yaw+pitch
	forward := mgl32.Vec3{
		float32(math.Cos(float64(pitch)) * math.Cos(float64(yaw))),
		float32(math.Cos(float64(pitch)) * math.Sin(float64(yaw))),
		float32(math.Sin(float64(pitch))),
	}

	// Right vector from yaw (Quake XY plane)
	right := mgl32.Vec3{float32(math.Sin(float64(yaw))), -float32(math.Cos(float64(yaw))), 0}

	// Apply bob to eye position
	pos = pos.Add(right.Mul(bobH))
	pos[2] += bobV

	target := pos.Add(forward)
	up := mgl32.Vec3{0, 0, 1}

	// Roll: tilt camera based on sideways velocity (max ~4 degrees), smoothed
	sideVel := p.Velocity[0]*right[0] + p.Velocity[1]*right[1]
	maxRoll := float32(4.0 * math.Pi / 180.0)
	targetRoll := sideVel / 320.0 * maxRoll // strafe right → tilt right
	rollSpeed := float32(8.0)               // higher = snappier, lower = more lag
	if dt > 0 {
		r.smoothedRoll += (targetRoll - r.smoothedRoll) * (1 - float32(math.Exp(float64(-rollSpeed*dt))))
	}
	up = up.Add(right.Mul(r.smoothedRoll)).Normalize()

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
	gl.ActiveTexture(gl.TEXTURE1)
	gl.BindTexture(gl.TEXTURE_2D, r.lightmapTexture)
	gl.Uniform1i(r.lightmapLoc, 1)

	// Draw world (PVS on, no entity offset)
	gl.Uniform3f(r.entityOffsetLoc, 0, 0, 0)
	gl.BindVertexArray(r.vao)
	gl.DrawArrays(gl.TRIANGLES, 0, r.numVerts)
	gl.BindVertexArray(0)

	// Draw brush entities (PVS off, per-entity offset)
	if len(p.Entities) > 0 {
		gl.Uniform1i(r.usePVSLoc, 0)
		for _, es := range p.Entities {
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

	// Draw skybox — rotation-only view so it's infinitely far away.
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

	// Draw world items and monsters — MDL/BSP models at their origins.
	if r.weaponProg != 0 && len(p.Items) > 0 {
		gl.Disable(gl.CULL_FACE)
		gl.UseProgram(r.weaponProg)
		gl.UniformMatrix4fv(r.weapProjLoc, 1, false, &proj[0])
		gl.Uniform1i(r.weapTexLoc, 0)
		gl.ActiveTexture(gl.TEXTURE0)
		for _, is := range p.Items {
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

	// Draw particles (blood + sparks) — alpha blended, depth write off.
	if r.particleProg != 0 && len(p.Particles) > 0 {
		r.particleScratch = r.particleScratch[:0]
		for _, pt := range p.Particles {
			stuck := float32(0)
			if pt.Stuck {
				stuck = 1
			}
			r.particleScratch = append(r.particleScratch, pt.Pos[0], pt.Pos[1], pt.Pos[2], pt.Life, stuck, float32(pt.Kind))
		}
		n := int32(len(p.Particles))
		gl.BindBuffer(gl.ARRAY_BUFFER, r.particleVBO)
		gl.BufferSubData(gl.ARRAY_BUFFER, 0, int(n)*24, unsafe.Pointer(&r.particleScratch[0]))
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)

		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.DepthMask(false)
		gl.Disable(gl.CULL_FACE)

		gl.UseProgram(r.particleProg)
		gl.UniformMatrix4fv(r.partMVPLoc, 1, false, &mvp[0])
		gl.BindVertexArray(r.particleVAO)
		gl.DrawArrays(gl.POINTS, 0, n)
		gl.BindVertexArray(0)

		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
		gl.Enable(gl.CULL_FACE)
	}

	// Draw bullet tracers — alpha blended lines, depth write off.
	if r.tracerProg != 0 && len(p.Tracers) > 0 {
		r.tracerScratch = r.tracerScratch[:0]
		for _, tr := range p.Tracers {
			r.tracerScratch = append(r.tracerScratch,
				tr.From[0], tr.From[1], tr.From[2], tr.Life,
				tr.To[0], tr.To[1], tr.To[2], tr.Life,
			)
		}
		n := int32(len(p.Tracers) * 2)
		gl.BindBuffer(gl.ARRAY_BUFFER, r.tracerVBO)
		gl.BufferSubData(gl.ARRAY_BUFFER, 0, int(n)*16, unsafe.Pointer(&r.tracerScratch[0]))
		gl.BindBuffer(gl.ARRAY_BUFFER, 0)

		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE)
		gl.DepthMask(false)
		gl.Disable(gl.CULL_FACE)

		gl.UseProgram(r.tracerProg)
		gl.UniformMatrix4fv(r.tracerMVPLoc, 1, false, &mvp[0])
		gl.BindVertexArray(r.tracerVAO)
		gl.DrawArrays(gl.LINES, 0, n)
		gl.BindVertexArray(0)

		gl.DepthMask(true)
		gl.Disable(gl.BLEND)
		gl.Enable(gl.CULL_FACE)
	}

	// Draw view weapon on top of everything — clear depth so it is never
	// occluded by world geometry.
	if r.weaponProg != 0 && len(r.weapons) > 0 {
		weaponSlot := p.Weapon
		if weaponSlot < 0 || weaponSlot >= len(r.weapons) {
			weaponSlot = 0
		}
		wr := r.weapons[weaponSlot]

		if len(wr.frames) > 0 {
			gl.Clear(gl.DEPTH_BUFFER_BIT)
			gl.Disable(gl.CULL_FACE)

			wfIdx := p.WeaponFrame
			if wfIdx < 0 || wfIdx >= len(wr.frames) {
				wfIdx = 0
			}
			wf := wr.frames[wfIdx]
			if wf.vao != 0 {
				rotZ := mgl32.HomogRotate3DZ(mgl32.DegToRad(90))
				rotX := mgl32.HomogRotate3DX(mgl32.DegToRad(-90))
				rot := rotX.Mul4(rotZ)

				// Weapon bob in camera space — slightly larger amplitude than view bob
				weapBobV := bobV * 1.5
				weapBobH := bobH * 1.2
				weapRoll := r.smoothedRoll * 0.75 // slightly less tilt than camera roll
				trans := mgl32.Translate3D(weapBobH, -10+weapBobV, -10)
				rollMat := mgl32.HomogRotate3DZ(weapRoll)
				weaponMat := trans.Mul4(rollMat).Mul4(rot)

				gl.UseProgram(r.weaponProg)
				gl.UniformMatrix4fv(r.weapProjLoc, 1, false, &proj[0])
				gl.UniformMatrix4fv(r.weapMatLoc, 1, false, &weaponMat[0])

				gl.ActiveTexture(gl.TEXTURE0)
				gl.BindTexture(gl.TEXTURE_2D, wr.tex)
				gl.Uniform1i(r.weapTexLoc, 0)

				gl.BindVertexArray(wf.vao)
				gl.DrawArrays(gl.TRIANGLES, 0, wf.numVerts)
				gl.BindVertexArray(0)
			}

			gl.Enable(gl.CULL_FACE)
		}
	}

	// Draw underwater tint — after weapon so it tints both world and weapon, before HUD.
	if r.underwaterProg != 0 && p.InWater {
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.CULL_FACE)
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)
		gl.UseProgram(r.underwaterProg)
		gl.Uniform1f(r.underwaterTimeLoc, float32(time.Since(r.startTime).Seconds()))
		gl.BindVertexArray(r.underwaterVAO)
		gl.DrawArrays(gl.TRIANGLES, 0, 6)
		gl.BindVertexArray(0)
		gl.Disable(gl.BLEND)
		gl.Enable(gl.CULL_FACE)
		gl.Enable(gl.DEPTH_TEST)
	}

	// Draw HUD — last, depth test disabled.
	if r.hudVAO != 0 {
		gl.Disable(gl.DEPTH_TEST)
		gl.Disable(gl.CULL_FACE)
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)

		if r.hudValid && r.hudProg != 0 {
			// Sprite atlas path.
			verts, nverts := r.buildHUDVerts(p)
			if nverts > 0 {
				gl.BindBuffer(gl.ARRAY_BUFFER, r.hudVBO)
				gl.BufferSubData(gl.ARRAY_BUFFER, 0, nverts*16, unsafe.Pointer(&verts[0]))
				gl.BindBuffer(gl.ARRAY_BUFFER, 0)
				gl.UseProgram(r.hudProg)
				gl.ActiveTexture(gl.TEXTURE0)
				gl.BindTexture(gl.TEXTURE_2D, r.hudAtlasTex)
				gl.Uniform1i(r.hudTexLoc, 0)
				gl.BindVertexArray(r.hudVAO)
				gl.DrawArrays(gl.TRIANGLES, 0, int32(nverts))
				gl.BindVertexArray(0)
				gl.BindTexture(gl.TEXTURE_2D, 0)
			}
		} else if r.hudGradProg != 0 {
			// Gradient fallback path: health (left third) | armor (middle third) | ammo (right third).
			gl.UseProgram(r.hudGradProg)
			gl.BindBuffer(gl.ARRAY_BUFFER, r.hudVBO)
			gl.BindVertexArray(r.hudVAO)

			drawBar := func(x0, x1, frac float32, kind int32) {
				verts := [24]float32{
					x0, -1, 0, 0,
					x1, -1, 1, 0,
					x1, -0.97, 1, 1,
					x0, -1, 0, 0,
					x1, -0.97, 1, 1,
					x0, -0.97, 0, 1,
				}
				gl.BufferSubData(gl.ARRAY_BUFFER, 0, len(verts)*4, unsafe.Pointer(&verts[0]))
				gl.Uniform1f(r.hudFracLoc, frac)
				gl.Uniform1i(r.hudKindLoc, kind)
				gl.DrawArrays(gl.TRIANGLES, 0, 6)
			}

			// Health bar: x ∈ [-1, -0.34]
			healthFrac := float32(p.Health) / 100.0
			if healthFrac < 0 {
				healthFrac = 0
			} else if healthFrac > 1 {
				healthFrac = 1
			}
			drawBar(-1, -0.34, healthFrac, 0)

			// Armor bar: x ∈ [-0.33, 0.33] — only drawn when player has armor
			if p.Armor > 0 {
				armorMax := float32(200) // item_armorInv cap
				armorFrac := float32(p.Armor) / armorMax
				if armorFrac > 1 {
					armorFrac = 1
				}
				drawBar(-0.33, 0.33, armorFrac, 1)
			}

			// Ammo bar: x ∈ [0.34, 1]
			cur, maxAmmo := p.CurrentWeaponAmmo()
			ammoFrac := float32(0)
			if maxAmmo > 0 {
				ammoFrac = float32(cur) / float32(maxAmmo)
				if ammoFrac < 0 {
					ammoFrac = 0
				} else if ammoFrac > 1 {
					ammoFrac = 1
				}
			}
			drawBar(0.34, 1, ammoFrac, 2)

			gl.BindVertexArray(0)
			gl.BindBuffer(gl.ARRAY_BUFFER, 0)
		}

		gl.Disable(gl.BLEND)
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
// Each vertex: x, y, z, faceIndex, s, t, lmU, lmV (8 floats; s/t are raw pixel-space texture coords).
func buildModelVerts(m *bsp.Map, modelIdx int, lmInfos []bsp.LightmapFaceInfo, lmW, lmH int) []float32 {
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

		var info bsp.LightmapFaceInfo
		if faceIdx < len(lmInfos) {
			info = lmInfos[faceIdx]
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
				// Lightmap UV: map texel-space s/t into atlas-normalized coords.
				ls := (s - info.MinS) / 16.0
				lt := (t - info.MinT) / 16.0
				lmU := (float32(info.AtlasX) + 0.5 + ls) / float32(lmW)
				lmV := (float32(info.AtlasY) + 0.5 + lt) / float32(lmH)
				verts = append(verts, vtx[0], vtx[1], vtx[2], fi, s, t, lmU, lmV)
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

// buildHUDAtlas packs the HUD LMP sprites into a single RGBA GL texture.
// Returns true if at least the sbar or some sprites were packed successfully.
func buildHUDAtlas(r *Renderer, a *HUDAssets) bool {
	const atlasW = 512

	// Measure row heights.
	sbarH := 0
	spriteH := 0
	if a.SBar != nil {
		sbarH = a.SBar.Height
	}
	for _, n := range a.Nums {
		if n != nil && n.Height > spriteH {
			spriteH = n.Height
		}
	}
	for _, f := range a.Faces {
		if f != nil && f.Height > spriteH {
			spriteH = f.Height
		}
	}
	if sbarH == 0 && spriteH == 0 {
		return false
	}

	// Measure weapon icon row height.
	iconH := 0
	for _, img := range a.WeaponsDim {
		if img != nil && img.Height > iconH {
			iconH = img.Height
		}
	}
	for _, img := range a.WeaponsLit {
		if img != nil && img.Height > iconH {
			iconH = img.Height
		}
	}
	if iconH == 0 {
		iconH = 24 // fallback
	}

	// Layout:
	//   row0: sbar (height = sbarH)
	//   row1: big digits + faces (height = spriteH)
	//   row2: dim weapon icons
	//   row3: lit weapon icons
	//   row4: gold texel (1px)
	//   row5: small ammo digits (8px)
	row1Y := sbarH
	if row1Y > 0 {
		row1Y++ // 1px padding
	}
	row2Y := row1Y + spriteH + 1
	row3Y := row2Y + iconH + 1
	row4Y := row3Y + iconH + 1
	row5Y := row4Y + 2
	rawH := row5Y + 8 + 1
	atlasH := 1
	for atlasH < rawH {
		atlasH <<= 1
	}
	invW := float32(1) / float32(atlasW)
	invH := float32(1) / float32(atlasH)

	pixels := make([]byte, atlasW*atlasH*4)

	blit := func(img *gfx.LMPImage, destX, destY int) {
		for py := 0; py < img.Height; py++ {
			for px := 0; px < img.Width; px++ {
				src := (py*img.Width + px) * 4
				dst := ((destY+py)*atlasW + (destX + px)) * 4
				if dst+3 < len(pixels) {
					pixels[dst+0] = img.RGBA[src+0]
					pixels[dst+1] = img.RGBA[src+1]
					pixels[dst+2] = img.RGBA[src+2]
					pixels[dst+3] = img.RGBA[src+3]
				}
			}
		}
	}

	// blitDim copies img with alpha multiplied by factor (0.0–1.0).
	blitDim := func(img *gfx.LMPImage, destX, destY int, factor float32) {
		for py := 0; py < img.Height; py++ {
			for px := 0; px < img.Width; px++ {
				src := (py*img.Width + px) * 4
				dst := ((destY+py)*atlasW + (destX + px)) * 4
				if dst+3 < len(pixels) {
					pixels[dst+0] = img.RGBA[src+0]
					pixels[dst+1] = img.RGBA[src+1]
					pixels[dst+2] = img.RGBA[src+2]
					a := float32(img.RGBA[src+3]) * factor
					if a > 255 {
						a = 255
					}
					pixels[dst+3] = byte(a)
				}
			}
		}
	}

	sprite := func(x, y, w, h int) hudSprite {
		return hudSprite{
			u0: float32(x) * invW,
			v0: float32(y) * invH,
			u1: float32(x+w) * invW,
			v1: float32(y+h) * invH,
			pw: float32(w),
			ph: float32(h),
		}
	}

	// Row 0: sbar.
	if a.SBar != nil {
		blit(a.SBar, 0, 0)
		r.hudSBar = sprite(0, 0, a.SBar.Width, a.SBar.Height)
		r.hudSBarW = float32(a.SBar.Width)
		r.hudSBarH = float32(a.SBar.Height)
	}

	// Row 1: digits then faces.
	const digitW = 24
	for i, n := range a.Nums {
		if n == nil {
			continue
		}
		dx := i * digitW
		blit(n, dx, row1Y)
		r.hudNums[i] = sprite(dx, row1Y, n.Width, n.Height)
	}
	const faceStartX = 10 * digitW // 240
	for i, f := range a.Faces {
		if f == nil {
			continue
		}
		fx := faceStartX + i*digitW
		blit(f, fx, row1Y)
		r.hudFaces[i] = sprite(fx, row1Y, f.Width, f.Height)
	}

	// Row 2: dim weapon icons (40% alpha for unowned look).
	const iconSlotW = 32
	for i, img := range a.WeaponsDim {
		if img == nil {
			continue
		}
		ix := i * iconSlotW
		blitDim(img, ix, row2Y, 0.4)
		r.hudWeapDim[i] = sprite(ix, row2Y, img.Width, img.Height)
	}

	// Row 3: lit weapon icons (full alpha for owned look).
	for i, img := range a.WeaponsLit {
		if img == nil {
			continue
		}
		ix := i * iconSlotW
		blit(img, ix, row3Y)
		r.hudWeapLit[i] = sprite(ix, row3Y, img.Width, img.Height)
	}

	// Row 4: gold selection texel at x=508.
	{
		const gx, gy = 508, 0
		goldY := row4Y + gy
		dst := (goldY*atlasW + gx) * 4
		if dst+3 < len(pixels) {
			pixels[dst+0] = 255
			pixels[dst+1] = 210
			pixels[dst+2] = 0
			pixels[dst+3] = 255
		}
		r.hudGoldTexel = sprite(gx, row4Y, 1, 1)
	}

	// Row 5: small ammo digits (8×8).
	for i, sn := range a.SmallNums {
		if sn == nil {
			continue
		}
		sx := i * 8
		blit(sn, sx, row5Y)
		r.hudSmallNums[i] = sprite(sx, row5Y, sn.Width, sn.Height)
	}

	// Store weapon bar layout parameters.
	r.hudWeapBarY = r.hudSBarH
	const weapBarH = float32(28)
	r.hudTotalH = r.hudSBarH + weapBarH

	// Upload atlas texture.
	gl.GenTextures(1, &r.hudAtlasTex)
	gl.BindTexture(gl.TEXTURE_2D, r.hudAtlasTex)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(atlasW), int32(atlasH), 0,
		gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&pixels[0]))
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.BindTexture(gl.TEXTURE_2D, 0)

	return true
}

// buildHUDVerts builds a flat float32 slice of HUD quads for this frame.
// Each quad is 6 vertices × 4 floats (x, y, u, v).
// Returns the slice and the vertex count.
func (r *Renderer) buildHUDVerts(p *physics.Physics) ([]float32, int) {
	// HUD occupies bottom strip in NDC.
	// Virtual HUD coords: 0..320 wide, 0..hudTotalH tall.
	refW := float32(320)
	refH := r.hudTotalH
	if refH <= 0 {
		refH = r.hudSBarH
	}
	if refH <= 0 {
		refH = 24
	}
	const hudNDCHeight = float32(0.20) // fraction of [-1,1] y range the HUD occupies

	ndcX := func(vx float32) float32 { return (vx/refW)*2.0 - 1.0 }
	ndcY := func(vy float32) float32 { return -1.0 + (vy/refH)*hudNDCHeight }

	var verts []float32

	quad := func(vx0, vy0, vx1, vy1 float32, sp hudSprite) {
		x0, y0 := ndcX(vx0), ndcY(vy0)
		x1, y1 := ndcX(vx1), ndcY(vy1)
		verts = append(verts,
			x0, y0, sp.u0, sp.v1,
			x1, y0, sp.u1, sp.v1,
			x1, y1, sp.u1, sp.v0,
			x0, y0, sp.u0, sp.v1,
			x1, y1, sp.u1, sp.v0,
			x0, y1, sp.u0, sp.v0,
		)
	}

	// 1. Status bar background.
	if r.hudSBarW > 0 && r.hudSBarH > 0 {
		quad(0, 0, r.hudSBarW, r.hudSBarH, r.hudSBar)
	}

	// 2. Health digits at vx=136 (three digits, right-justified).
	health := p.Health
	if health < 0 {
		health = 0
	}
	if health > 999 {
		health = 999
	}
	drawDigits := func(value, vx int) {
		d2 := value / 100
		d1 := (value / 10) % 10
		d0 := value % 10
		const dw = float32(24)
		const dh = float32(24)
		quad(float32(vx), 0, float32(vx)+dw, dh, r.hudNums[d2])
		quad(float32(vx)+dw, 0, float32(vx)+dw*2, dh, r.hudNums[d1])
		quad(float32(vx)+dw*2, 0, float32(vx)+dw*3, dh, r.hudNums[d0])
	}
	drawDigits(health, 136)

	// 3. Face sprite — health range: 0 = high, 4 = low.
	faceIdx := (100 - health) / 20
	if faceIdx > 4 {
		faceIdx = 4
	}
	if faceIdx < 0 {
		faceIdx = 0
	}
	if r.hudFaces[faceIdx].u1 > r.hudFaces[faceIdx].u0 {
		quad(112, 0, 136, 24, r.hudFaces[faceIdx])
	}

	// 4. Weapon inventory bar (above the sbar).
	if r.hudWeapBarY > 0 || r.hudTotalH > r.hudSBarH {
		const slotW = float32(32)
		const iconH = float32(24)
		const barX0 = float32(32)
		barY0 := r.hudWeapBarY
		barY1 := barY0 + iconH

		for slot := 0; slot < 8; slot++ {
			slotX0 := barX0 + float32(slot)*slotW
			slotX1 := slotX0 + slotW

			// Gold border for active weapon slot.
			if slot == p.Weapon && r.hudGoldTexel.u1 > r.hudGoldTexel.u0 {
				quad(slotX0-2, barY0-2, slotX1+2, barY1+2, r.hudGoldTexel)
			}

			// Skip unowned slots entirely.
			if !p.HasWeapon(slot) {
				continue
			}

			// Icon quad (lit = owned).
			sp := r.hudWeapLit[slot]
			if sp.u1 > sp.u0 {
				// Centre icon horizontally within the 32px slot.
				iconOffset := (slotW - sp.pw) / 2
				quad(slotX0+iconOffset, barY0, slotX0+iconOffset+sp.pw, barY1, sp)
			}

			// Ammo count (2 small digits) in bottom-right of each non-axe slot.
			if slot > 0 {
				slotAmmo := p.AmmoForSlot(slot)
				if slotAmmo < 0 {
					slotAmmo = 0
				}
				if slotAmmo > 99 {
					slotAmmo = 99
				}
				d1 := slotAmmo / 10
				d0 := slotAmmo % 10
				const dw = float32(8)
				const dh = float32(8)
				digitX0 := slotX1 - dw*2
				digitY0 := barY0
				if r.hudSmallNums[d1].u1 > r.hudSmallNums[d1].u0 {
					quad(digitX0, digitY0, digitX0+dw, digitY0+dh, r.hudSmallNums[d1])
				}
				if r.hudSmallNums[d0].u1 > r.hudSmallNums[d0].u0 {
					quad(digitX0+dw, digitY0, digitX0+dw*2, digitY0+dh, r.hudSmallNums[d0])
				}
			}
		}
	}

	return verts, len(verts) / 4
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
