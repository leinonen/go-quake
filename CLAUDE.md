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

Controls: WASD to move, mouse to look, Space to jump, left-click to attack (axe swing or shoot), keys 1–8 to switch weapons, Escape to quit.
Title bar shows current HP, leaf index, and player position.

## package structure

```
main.go              — LockOSThread, bus wiring, render loop
game/
  messages.go        — inter-goroutine message types (InputEvent, PlayerState, EntityState, RenderFrame, ItemState)
  bus.go             — Bus struct with all channels
bsp/
  types.go           — BSP29 on-disk structs (DLeaf, DNode, DFace, DEdge, DPlane, DModel)
  loader.go          — parses BSP from file path or []byte (for PAK extraction)
  entities.go        — entity lump parser: ParseEntities, ParseVec3, ParseFloat, MoveDir
  clip.go            — BSP hull tracing (HullTrace, HullPointContents)
  lighting.go        — FaceBrightness: per-face lightmap average (0–1) + sky/water sentinels (2.0/3.0); BuildLightmapAtlas: shelf-packs all face lightmaps into a 2048×N RGB atlas (BSP29 = 1 byte/texel grayscale, replicated to RGB)
pak/
  pak.go             — reads id Software PAK archives; FindMaps(), ReadFile()
vis/
  vis.go             — PVS RLE decompress, IsLeafVisible, LeafForPoint
entities/
  entities.go        — BrushEntity state machines (func_door, func_plat); Manager.Update, Manager.States
  items.go           — ParseItems, ItemPath: maps item classnames to PAK model paths (MDL + BSP sub-models); ParseMonsters, MonsterPath: maps monster_* classnames to MDL paths; FlamePath, ParseFlames: maps light_flame_* classnames to flame2.mdl; ItemSpawn.HealthValue for health packs
  monsters.go        — MonsterState runtime struct (Pos, VelZ, HP, Alerted, FrameIdx, AttackCooldown); NewMonsterState; FlameState runtime struct (Pos, MdlIdx, FrameIdx, FrameTime, NumFrames); AI constants
sound/
  sound.go           — OpenAL Manager: Init/Cleanup, 16-source pool, Play(SoundEvent), PlayPath(pakPath); missing files silently skipped
  wav.go             — RIFF/WAV decoder: 8/16-bit mono/stereo PCM → OpenAL buffer
mdl/
  mdl.go             — Quake MDL v6 parser: skins, texcoords, triangles, frames; BuildVerts(frameIdx), SkinRGB, NumFrames
renderer/
  renderer.go        — GL init, world + entity VAO upload, multi-frame item/weapon VAOs, HUD bar, draw loop
  compute.go         — SSBO lifecycle, per-frame dispatch + barrier
  shaders/
    pvs_traverse.glsl  — compute shader: Quake RLE PVS decode on GPU, sets visibleFaceFlags[]
    world.vert.glsl    — perspective projection + uEntityOffset for brush entity rendering; passes lightmap UV (aLightmapST) to fragment stage
    world.frag.glsl    — per-face texture from atlas; lightmap sampled from uLightmap atlas (overbright ×2); sky faces discard; water faces procedural; discards invisible faces
    skybox.vert.glsl   — passes cube vertex as vDir; sets gl_Position.z = w (depth = 1.0)
    skybox.frag.glsl   — procedural ominous sky: direction-based FBM clouds, horizon ember glow
    weapon.vert.glsl   — view-space weapon transform: uProj * uWeaponMat * aPos; also used for world items/monsters
    weapon.frag.glsl   — skin texture with grey desaturation + ambient dim to match world look
    hud.vert.glsl      — emits NDC quad vertices, passes vUV across bar width
    hud.frag.glsl      — health bar: discards pixels where vUV.x > uFrac; colour transitions green→red
    particle.vert.glsl — GL_POINTS with depth-scaled point size (2–16px); passes vLife, vStuck, vKind
    particle.frag.glsl — circular disc via gl_PointCoord; branches on vKind: blood = red, spark = orange→grey; alpha = edge * vLife
    underwater.vert.glsl — fullscreen NDC quad passthrough; computes vUV from aPos
    underwater.frag.glsl — blue-green tint (vec4(0.0, 0.25, 0.45, 0.35)) with animated ripple via uTime
    tracer.vert.glsl   — passes world-space aPos through uMVP; passes aLife as vLife
    tracer.frag.glsl   — bright yellow-white line with alpha = vLife * 0.85; additively blended
physics/
  physics.go         — WASD + mouselook, gravity, jumping, BSP collision, weapon swing, monster AI; own goroutine
input/
  input.go           — GLFW key/mouse snapshot pump (keys + mouse buttons 0–7)
```

## channel architecture

```
input (main thread) → bus.Input → physics goroutine → bus.Physics → main → bus.Render → renderer
```

- `bus.Input` unbuffered — honest backpressure on physics
- `bus.Physics` / `bus.Render` buffered 1 — stale states are dropped, never queued
- `bus.ItemPickups` buffered 16 — physics sends item indices on pickup (items only); main drains each frame
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
- **Monster collision:** `monsterMoveTrace` uses `HeadNodes[0]` (point hull) for both world and entities; monsters are blocked by closed doors
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

## underwater tint

When the player's eye is inside a water leaf, a full-screen blue-green overlay is drawn over the entire frame including the view weapon.

- **Detection:** `physics/physics.go` checks `m.Leaves[leafIndex].Contents == ContentsWater` after every `LeafForPoint` call (init, `tick()`, respawn, noclip); result stored in `PlayerState.InWater`
- **Shaders:** `underwater.vert.glsl` emits a fullscreen NDC quad; `underwater.frag.glsl` outputs `vec4(0.0, 0.25+wave, 0.45+wave, 0.35)` with a subtle animated ripple via `uTime`
- **Draw position:** after weapon depth-clear and weapon draw, before HUD — so both world and weapon receive the tint but the HUD remains unaffected

## lightmap atlas

BSP29 lightmaps are 1 byte per texel (grayscale luminance). `BuildLightmapAtlas` in `bsp/lighting.go` shelf-packs all face lightmaps into a single `2048×N` RGB texture (grayscale replicated to all 3 channels):

- Faces without valid lightmap data (sky/water sentinels, `LightOfs < 0`, out-of-bounds) map to a `128,128,128` fallback texel at `(0,0)`; `×2.0` in the shader = full brightness
- Shelf-pack cursor starts at `x=2` to reserve `(0,0)` for the fallback; each shelf row adds 1-texel padding on all sides to prevent bleeding under bilinear filtering
- `LightmapFaceInfo` stores `AtlasX/Y` (inside padding), `W/H`, and `MinS/MinT` (lightmap-space origin in texel units × 16)
- Per-vertex lightmap UVs computed in `buildModelVerts`: `lmU = (AtlasX + 0.5 + (s − MinS)/16) / atlasW`
- Uploaded as `GL_TEXTURE1` with `GL_LINEAR` + `GL_CLAMP_TO_EDGE`; world and entity draw passes bind it before drawing
- Fragment shader: `color * texture(uLightmap, vLightmapST).rgb * 2.0` (Quake overbright factor)
- Vertex format: 8 floats per vertex (`x,y,z, faceIdx, s,t, lmU,lmV`), stride 32; lightmap UV is attribute location 3
- `FaceBrightness` SSBO (binding 4) is retained; the fragment shader still reads it for sky/water sentinel branching only

## view weapon rendering

`progs/v_axe.mdl` is loaded from the PAK at startup via `mdl.Load`. All animation frames are built into separate VAOs (interleaved `x,y,z,u,v`, 5 floats per vertex, no index buffer). The skin texture is uploaded once and shared across frames.

Draw order: world → brush entities → skybox → items/monsters → **particles** (blend on, depth write off) → **tracers** (additive blend, depth write off) → (depth clear) → **weapon** → **underwater tint** → **HUD**.

The weapon is positioned in GL camera space via:
- `RotX(-90) * RotZ(90)` — converts Quake view space (X=forward, -Y=right, Z=up) to GL camera space (-Z=forward, +X=right, +Y=up)
- `Translate3D(0, -10, -10)` — places it in the lower-centre of the view

The active VAO is selected each frame by `frame.Player.WeaponFrame`. If `progs/v_axe.mdl` is absent (standalone `.bsp` load), weapon rendering is silently skipped.

## weapon firing

`tickWeapon` dispatches to the active weapon's tick each physics frame. Weapons switch via keys 1–8 (slot must be owned and have ammo).

**Axe (slot 0):** melee swing driven by MDL animation
- Left mouse button starts swing at frame 1; frames advance at 8 FPS
- `tryAxeHit` fires on `weaponFrame >= 2` (once per swing): casts a 64-unit ray from eye, deals 25 damage to monsters within 40 units of ray tip
- Swing ends when `weaponFrame` reaches `NumFrames`; returns to frame 0

**Hitscan weapons (slots 1–7):** `fireHitscan(numPellets, spread, damage)` casts rays from eye
- Shotgun (1): semi-auto, 1 shell, spread pellets, 4 damage each
- Super shotgun (2): semi-auto, 2 shells, more pellets, higher spread
- Nailgun (3): full-auto, 1 nail/shot, 9 damage
- Super nailgun (4): full-auto, 2 nails/shot, 18 damage
- Rocket/grenade launcher (5): semi-auto, simulated via hitscan, 100–120 damage
- Lightning gun (7): full-auto, 1 cell/shot, 30 damage

Auto-switch (`autoSwitchWeapon`) triggers when the current weapon runs out of ammo.

## item pickup system

Item spawns (weapons, armor, ammo, health, keys) are loaded from the BSP entity lump at startup into `itemSpawns []entities.ItemSpawn` and `itemStates []game.ItemState`. Monsters are managed separately by physics and sent each frame via `PlayerState.MonsterItems`.

**Pickup detection** runs in the physics goroutine:
- After each movement tick, the player foot origin (`position − eyeHeight`) is checked against each unpicked item
- Radius: 32 units (Quake standard)
- On contact: if `item.HealthValue > 0` the player's HP is increased (capped at 100); index sent on `bus.ItemPickups` (non-blocking); `picked[i] = true` prevents double-pickup

**Main loop** drains `bus.ItemPickups` each frame, marks `pickedItems[idx] = true`, then builds `visibleItems` from unpicked items plus `playerState.MonsterItems`.

**Single-player rules**: no item respawn. Items are gone permanently once picked.

## monster AI system

Monster state lives entirely in `entities.MonsterState` slices owned by the physics goroutine. Each tick, `tickMonsters` runs:

- **Animation:** `FrameIdx` advances at 10 FPS (wraps mod `NumFrames`)
- **Alert:** if within 1024 units and `HullTrace` (point hull, world only) finds clear LOS to player foot, `Alerted = true`
- **Chase:** when alerted and outside melee range, XY movement is traced via `monsterMoveTrace` (point hull, world + entities); blocked by closed doors and walls
- **Gravity:** `VelZ` accumulates downward at 800 units/s²; vertical movement traced via `monsterMoveTrace`; `VelZ` resets to 0 on landing or ceiling impact
- **Melee:** within 64 units, deals 10 damage per hit with 1.5 s cooldown
- **Death:** `HP ≤ 0` sets `Dead = true`; monster is excluded from `MonsterItems` and disappears from the scene

Live monsters are communicated to the renderer each frame as `PlayerState.MonsterItems []ItemState`, where each entry carries `Pos`, `MdlIdx`, and `Frame`. The renderer indexes into `itemVAOs[MdlIdx].frames[Frame]` to draw the correct animation frame.

## player health and respawn

- Player starts at 100 HP
- Health packs restore 25 HP (normal) or 100 HP (megahealth), capped at 100
- Monster melee deals 10 HP per hit
- `PlayerState.Health` is published each physics tick; the HUD bar reads it
- On death (`playerHP ≤ 0`): player teleports to spawn, velocity zeroed, HP reset to 100, all monsters un-alert

## HUD health bar

Drawn last (after weapon), depth test disabled. A static NDC quad covers the bottom strip of the screen (`y ∈ [−1, −0.97]`). The fragment shader discards pixels where `vUV.x > uFrac` where `uFrac = Health / 100.0`. Colour transitions from green (full health) to red (low health) as `uFrac` decreases.

## multi-frame MDL rendering

`renderer.ItemModel` holds `Frames [][]*WeaponMesh` — one slice of texture groups per animation frame. At `Init`, each MDL frame is uploaded as a separate VAO (same skin texture reused across frames). `itemRenderable.frames[frameIdx][groupIdx]` selects the right VAO at draw time. BSP sub-model items have a single frame. The weapon uses `[]weaponFrameData` (one VAO per frame, shared texture).

## special features

- Compute shader PVS: GLQuake's portal/PVS visibility approach executed on the GPU as compute — not rasterization, not raytracing. Unusual middle ground.
- Lightmap atlas: all per-face baked lightmaps (BSP29 grayscale, 1 byte/texel) shelf-packed into a GPU atlas texture and sampled per-pixel; produces smooth spatial lighting gradients as Quake intended.
- Emergent game loop: no central tick. Input, physics, and rendering are goroutines communicating via typed channels. Vsync (`SwapInterval(1)`) is the only throttle.
- Interactive doors and elevators: proximity-triggered state machines with BSP hull collision.
- Procedural skybox: direction-based FBM replaces Quake sky polygons entirely; no visible seams from any angle.
- Procedural water: sin-warp + FBM replaces Quake water textures with animated caustics; screen-space blue-green tint when submerged.
- View weapon: `v_axe.mdl` rendered in camera space with full swing animation and hit detection; hitscan weapons (shotgun through lightning gun) share the same firing input and auto-switch on empty ammo.
- Item pickup: weapons, armor, ammo, health, and keys disappear on contact; health packs restore HP.
- Monster AI: alert on LOS, chase with collision, gravity, melee attack, death; driven entirely in the physics goroutine.
- Player respawn: death teleports back to spawn with full HP and reset monster alert state.
- HUD health bar: NDC quad at screen bottom, green→red colour transition, driven by `uFrac` uniform.
- Blood particles: axe hits spray ~80 physics-simulated GL_POINTS in a wide cone; pool of 2048 shared with sparks; each particle arcs under gravity and collides with BSP geometry via `HullTrace`; stuck decals linger ~7s then fade; rendered with alpha blending after skybox before weapon depth-clear.
- Wall sparks: hitscan pellets that strike BSP geometry emit 12 orange spark particles (`particleKindSpark`) per pellet; share the 2048-slot particle pool with blood; stuck decals fade in 2s; fragment shader branches on `vKind` for colour (orange→grey vs red).
- Bullet tracers: each hitscan pellet emits a `tracer` line segment from the weapon muzzle (`muzzlePos`: eye + forward×10 − up×10) to the impact point; pool of 128; lifetime 50 ms; exported as `[]TracerState` (normalised life); rendered as `GL_LINES` with additive blending (`GL_ONE`) in `tracer.vert/frag.glsl`.
- Flame entities: `light_flame_large_yellow`, `light_flame_small_yellow`, `light_flame_large_white`, `light_flame_small_white` parsed from the entity lump; rendered as looping `flame2.mdl` animation via the existing `itemVAOs` path; no AI, no collision, no pickup.
- Sound: OpenAL audio for all weapons (axe, shotgun, super shotgun, nailgun, super nailgun, rocket, grenade, lightning), item pickup, and per-monster death cries; 16-source pool; accumulated in physics goroutine and drained by main each frame; silently skipped if PAK or OpenAL unavailable.

## particle system

Pool of 2048 `particle` structs (blood + sparks) owned by the physics goroutine (`physics/physics.go`). Each `particle` carries a `Kind` field (`particleKindBlood=0`, `particleKindSpark=1`). Each tick, `tickParticles` runs:
- **Life decay:** `Life -= dt`; deactivate when `Life ≤ 0`
- **Flying:** gravity applied (`Vel[2] -= 800*dt`), then `HullTrace` against world point hull
  - Hit → `Pos = tr.EndPos`, zero velocity, `Stuck = true`, clamp `Life = min(Life, stuckLife)` where `stuckLife` is 7s for blood, 2s for sparks
  - `StartSolid` → mark stuck immediately
  - No hit → advance `Pos`
- **Stuck:** no movement; just fade

**Blood** (`emitBloodParticles`): called from `tryAxeHit` on monster hit and from `fireHitscan` on monster hit:
- Scans `particles[]` from `nextFreeHint` (amortised cursor) for free slots
- Sprays `particleEmitCount=80` particles with random cone spread (`particleSpread=1.4`) around the forward vector; speed `350 * rand(0.5..1.0)` units/s

**Sparks** (`emitWallSparks`): called from `fireHitscan` when a pellet hits BSP geometry (no closer monster hit):
- Sprays `sparkEmitCount=12` particles in the hemisphere around the surface normal; speed `300 * rand(0.5..1.0)` units/s; spread factor 1.2

Live particles flow to the renderer via `PlayerState.Particles []ParticleState` (normalised life + stuck + kind). Renderer packs 6 floats per particle `(x,y,z,life,stuck,kind)` into a dynamic VBO; particle fragment shader branches on `vKind` for colour.

## bullet tracer system

Pool of 128 `tracer` structs owned by the physics goroutine. Each tracer is a world-space line segment with a 50 ms lifetime.

**Emission** (`emitTracer`): called from `fireHitscan` once per pellet after the world trace. Start point is `muzzlePos(eye, fwd, yaw, pitch)` = eye + forward×10 − up×10, matching the weapon render offset `Translate3D(0, -10, -10)` in GL camera space. End point is the impact point (world trace hit, monster position, or ray end).

**Tick** (`tickTracers`): `Life -= dt`; deactivated when `Life ≤ 0`.

**Export** (`buildTracerItems`): active tracers collected into `Physics.Tracers []TracerState` (From, To, normalised Life).

**Rendering**: packed as 2 vertices per tracer × 4 floats `(x,y,z,life)`; drawn as `GL_LINES` with additive blending (`GL_SRC_ALPHA, GL_ONE`), depth write off. Fragment shader fades white→yellow as life decays.

## flame entity system

`light_flame_large_yellow`, `light_flame_small_yellow`, `light_flame_large_white`, `light_flame_small_white` are decorative fire entities found in maps like `start`. They all share `progs/flame2.mdl`.

**Loading** (in `main.go`, after monster loading):
- `entities.ParseFlames(m.Entities)` extracts flame spawns from the entity lump
- Each unique MDL path is loaded with `loadMDLAllFrames` and added to `itemModels` (shared `modelPathToIdx` deduplicates across items/monsters/flames)
- A `FlameState` is created per spawn with `Pos`, `MdlIdx`, and `NumFrames`; no MDL frame names needed (no AI state machine)

**Animation** (`tickFlames` in `physics/physics.go`):
- Called each tick in both `tick()` and `noclip()` after `tickParticles`
- `FrameTime += dt * MonsterFPS`; wraps `FrameIdx` mod `NumFrames`
- Purely time-driven — no gravity, no collision, no alert radius

**Rendering**: flames are appended to `PlayerState.MonsterItems` each tick (after monsters), using the same `game.ItemState{Pos, MdlIdx, Frame}` struct. The renderer draws them identically to item/monster MDLs with no changes required.

## sound system

OpenAL-based audio via cgo (`-lopenal`). All sounds are loaded from the PAK file at startup; missing files are silently skipped so the engine runs without audio on systems without a PAK or OpenAL.

**Sound events** (`sound.SoundEvent` enum):
- Weapon fire: `SndAxeSwing`, `SndAxeHit`, `SndShotgun`, `SndSuperShotgun`, `SndNailgun`, `SndSuperNailgun`, `SndRocket`, `SndGrenade`, `SndLightning`
- `SndItemPickup` — any item contact

**Monster death sounds**: path-keyed via `entities.MonsterDeathSound(mdlPath)` → `monsterDeathSounds` map (soldier, dog, etc.). Loaded at startup via `sound.PreloadPaths`; played via `sound.PlayPath(path)`.

**Integration**: physics goroutine accumulates `PlayerState.SoundEvents []SoundEvent` and `PlayerState.SoundPaths []string` during each tick; main loop drains both slices each frame and calls `sound.Play` / `sound.PlayPath`.

**Source pool**: 16 pre-allocated `ALuint` sources. `playBuf` finds a stopped/initial source; if all are busy the sound is silently dropped (non-blocking).

## current limitations / next steps

- `CountVisible()` does a GPU→CPU readback every 30 frames (debug only); replace with `glMultiDrawArraysIndirect` for fully GPU-resident pipeline
- Doors linked by `target`/`targetname` are not grouped (each panel opens independently)
- No view bob or weapon kick animation
- Monster AI is purely melee — no ranged attacks, no projectiles
- Monsters have no death animation (disappear instantly on HP ≤ 0)
- No enemy variety in combat behaviour (all monsters use identical melee AI regardless of type)
- Items do not respawn (single-player rules)
