# go quake

Minimalistic Quake engine written in Go. Loads real Quake 1 maps (BSP29) and renders them using compute shader PVS culling with a goroutine channel architecture.

## running

```bash
# Load map from PAK file (Quake 1 installation)
go run . -pak /path/to/id1/pak0.pak -map e1m1

# List all maps in a PAK
go run . -pak /path/to/id1/pak0.pak

# Load a loose .bsp file
go run . -map /path/to/e1m1.bsp
```

Controls: WASD to move, mouse to look, Space to jump, Escape to quit.
Title bar shows current leaf index and player position.

## package structure

```
main.go              — LockOSThread, bus wiring, render loop
game/
  messages.go        — inter-goroutine message types (InputEvent, PlayerState, EntityState, RenderFrame)
  bus.go             — Bus struct with all channels
bsp/
  types.go           — BSP29 on-disk structs (DLeaf, DNode, DFace, DEdge, DPlane, DModel)
  loader.go          — parses BSP from file path or []byte (for PAK extraction)
  entities.go        — entity lump parser: ParseEntities, ParseVec3, ParseFloat, MoveDir
  clip.go            — BSP hull tracing (HullTrace, HullPointContents)
pak/
  pak.go             — reads id Software PAK archives; FindMaps(), ReadFile()
vis/
  vis.go             — PVS RLE decompress, IsLeafVisible, LeafForPoint
entities/
  entities.go        — BrushEntity state machines (func_door, func_plat); Manager.Update, Manager.States
mdl/
  mdl.go             — Quake MDL v6 parser: skins, texcoords, triangles, frames; BuildVerts, SkinRGB
renderer/
  renderer.go        — GL init, world + entity VAO upload, skybox cube VAO, weapon VAO, draw loop
  compute.go         — SSBO lifecycle, per-frame dispatch + barrier
  shaders/
    pvs_traverse.glsl  — compute shader: Quake RLE PVS decode on GPU, sets visibleFaceFlags[]
    world.vert.glsl    — perspective projection + uEntityOffset for brush entity rendering
    world.frag.glsl    — per-face texture from atlas; sky faces discard; water faces procedural; discards invisible faces
    skybox.vert.glsl   — passes cube vertex as vDir; sets gl_Position.z = w (depth = 1.0)
    skybox.frag.glsl   — procedural ominous sky: direction-based FBM clouds, horizon ember glow
    weapon.vert.glsl   — view-space weapon transform: uProj * uWeaponMat * aPos
    weapon.frag.glsl   — skin texture with grey desaturation + ambient dim to match world look
physics/
  physics.go         — WASD + mouselook, gravity, jumping, BSP collision; own goroutine
input/
  input.go           — GLFW key/mouse snapshot pump
```

## channel architecture

```
input (main thread) → bus.Input → physics goroutine → bus.Physics → main → bus.Render → renderer
```

- `bus.Input` unbuffered — honest backpressure on physics
- `bus.Physics` / `bus.Render` buffered 1 — stale states are dropped, never queued
- `bus.Shutdown` closed to broadcast stop to all goroutines
- GL must stay on the OS main thread (`runtime.LockOSThread` in `init()`)

## compute shader PVS design

SSBOs uploaded once at load time:
- binding 0: raw PVS bytes (RLE-compressed lump 4)
- binding 1: leaf descriptors `{ visofs, firstMarkSurface, numMarkSurfaces }`
- binding 2: marksurface indices
- binding 3 (output): `visibleFaceFlags[]` — zeroed each frame, set by compute

UBO per frame: `{ currentLeaf, totalLeafs }`. Dispatch: `ceil(totalLeafs/64)` groups × `local_size_x=64`.
After dispatch: `glMemoryBarrier(GL_SHADER_STORAGE_BARRIER_BIT)`, then fragment shader discards invisible faces.

Brush entities bypass PVS (`uUsePVS=0` during entity draw pass) since their faces are not in world mark-surfaces.

## brush entity system (func_door / func_plat)

State machine per entity: `Closed → Opening → Open → Closing → Closed`.

- **Trigger:** player foot origin within 64 units of entity AABB (expanded by 64 on all sides)
- **func_door:** moves along `angle` direction by `(bbox_extent_along_dir − lip)` units; default speed=100, wait=3s
- **func_plat:** geometry stored at top in BSP; starts at bottom (`offset = {0,0,−height}`), rises to top on trigger
- **Collision:** `traceAll` in physics traces world hull + each entity's `HeadNodes[1]` hull (offset into entity local space); player cannot walk through closed/moving doors
- **Rendering:** per-entity VAO built at init from `Models[N]` faces; `uEntityOffset` uniform shifts vertices each frame

## skybox rendering

Sky polygon faces (BSP texture prefix `sky`, brightness sentinel 2.0) are discarded in `world.frag.glsl`, punching holes in the depth buffer (depth stays at clear value 1.0).

The skybox is drawn last:
- Rotation-only view matrix (translation stripped) so it appears infinitely far
- `gl_Position.z = gl_Position.w` in vertex shader → fragment depth always = 1.0
- `glDepthFunc(GL_LEQUAL)` → skybox only fills pixels where nothing closer was drawn (i.e. the sky holes)
- Face culling disabled for the draw call; restored afterward

Skybox fragment shader uses `vDir = aPos` (cube vertex interpolated as world-space direction):
- `dir.z` = elevation (Quake Z-up); drives horizon fade and above/below split
- Sky-plane projection `dir.xy / max(dir.z, 0.05)` makes clouds recede toward the horizon
- Three FBM layers: slow void, rolling crimson masses, fast ember-orange/magenta veins
- Ember glow band at `elev ≈ 0`, void below horizon

## procedural water

Water faces (BSP texture prefix `*`, brightness sentinel 3.0) bypass the atlas and are shaded procedurally in `world.frag.glsl`:
- Two FBM layers sampled through sin-warp distorted UVs (Quake-style turbulence)
- Dark murky teal base with sparse caustic glints at wave crests
- Foam-edge highlight where the two wave systems clash (`abs(w1 - w2)`)
- Animated via `uTime` uniform (elapsed seconds since renderer init)

## view weapon rendering

`progs/v_axe.mdl` is loaded from the PAK at startup via `mdl.Load`. Frame 0 (idle) is built into a VAO with interleaved `x,y,z,u,v` (5 floats per vertex, no index buffer).

Draw order: world → brush entities → skybox → **weapon** (depth cleared before weapon draw so it is never occluded by world geometry).

The weapon is positioned in GL camera space via:
- `RotX(-90) * RotZ(90)` — converts Quake view space (X=forward, -Y=right, Z=up) to GL camera space (-Z=forward, +X=right, +Y=up)
- `Translate3D(16, -10, -25)` — places it in the lower-right of the view; tune `(right, up, forward)` in camera units

The fragment shader applies the same grey desaturation as `world.frag.glsl` (`mix(color, luma, 0.4)`) plus a fixed 0.72 ambient dim (no BSP lightmap for MDL models). If `progs/v_axe.mdl` is absent (standalone `.bsp` load), weapon rendering is silently skipped.

## special features

- Compute shader PVS: GLQuake's portal/PVS visibility approach executed on the GPU as compute — not rasterization, not raytracing. Unusual middle ground.
- Emergent game loop: no central tick. Input, physics, and rendering are goroutines communicating via typed channels. Vsync (`SwapInterval(1)`) is the only throttle.
- Interactive doors and elevators: proximity-triggered state machines with BSP hull collision.
- Procedural skybox: direction-based FBM replaces Quake sky polygons entirely; no visible seams from any angle.
- Procedural water: sin-warp + FBM replaces Quake water textures with animated caustics.
- View weapon: `v_axe.mdl` rendered in camera space with matching grey aesthetic.

## current limitations / next steps

- `CountVisible()` does a GPU→CPU readback every 30 frames (debug only); replace with `glMultiDrawArraysIndirect` for fully GPU-resident pipeline
- No sound
- Doors linked by `target`/`targetname` are not grouped (each panel opens independently)
- No monster/item entities
- Weapon renders frame 0 only — no swing animation, no view bob
