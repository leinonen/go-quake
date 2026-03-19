package physics

import (
	"math"
	"math/rand"

	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/entities"
	"go-quake/sound"
)

const (
	mouseSens      = 0.15
	moveSpeed      = 320.0 // units/s, classic Quake speed
	eyeHeight      = 22.0  // view height above player origin (Quake DEFAULT_VIEWHEIGHT)
	gravity        = 800.0 // units/s^2
	jumpSpeed      = 270.0 // initial Z velocity on jump
	pickupRadius   = 32.0  // item touch radius (Quake standard)
	weaponFPS      = 8.0   // weapon animation frames per second
	weaponHitFrame = 2     // axe MDL frame index at which hit detection fires

	particleCount     = 2048
	particleFlyLife   = 4.0   // seconds airborne
	particleStuckLife = 7.0   // seconds as decal
	particleSpeed     = 350.0 // units/s base spray speed
	particleSpread    = 1.4   // lateral cone factor
	particleEmitCount = 80    // per hit

	particleKindBlood = 0
	particleKindSpark = 1

	sparkEmitCount = 12    // sparks per pellet wall hit
	sparkFlyLife   = 1.5   // seconds airborne
	sparkStuckLife = 2.0   // seconds as decal
	sparkSpeed     = 300.0 // units/s base
	sparkSpread    = 1.2   // lateral cone factor

	tracerCount    = 128
	tracerLifetime = 0.05 // seconds (very brief flash)

	monsterSepRadius  = 28.0 // minimum XY distance between monster centers
	monsterHullOffset = 24   // hull 1 bottom extent below entity origin (Quake standing hull)
)

// ItemState carries the world position, mesh index, animation frame, and facing yaw for one item or monster.
type ItemState struct {
	Pos    [3]float32
	MdlIdx int
	Frame  int
	Yaw    float32 // facing angle in radians (world Z rotation); 0 = +X direction
}

// ParticleState carries the world position and lifetime for one particle.
type ParticleState struct {
	Pos   [3]float32
	Life  float32 // normalised 0..1 remaining lifetime (for alpha fade)
	Stuck bool
	Kind  uint8 // particleKindBlood or particleKindSpark
}

// TracerState carries the world-space endpoints and normalised lifetime for one bullet tracer.
type TracerState struct {
	From, To [3]float32
	Life     float32 // normalised 0..1
}

// particle is an internal particle (blood or spark).
type particle struct {
	Pos, Vel [3]float32
	Life     float32
	MaxLife  float32
	Stuck    bool
	Active   bool
	Kind     uint8
}

// tracer is an internal bullet tracer line segment.
type tracer struct {
	From, To [3]float32
	Life     float32
	Active   bool
}

// inputSnapshot is a single-frame snapshot of keyboard and mouse state.
type inputSnapshot struct {
	Keys         [512]bool
	MouseButtons [8]bool
	DX, DY       float64
}

// Physics holds all mutable physics state and is queried each frame by the renderer.
type Physics struct {
	// Exported — read by renderer each frame
	Pos        mgl32.Vec3
	Velocity   mgl32.Vec3
	Yaw, Pitch float32
	LeafIndex  int
	OnGround   bool
	Health     int
	WeaponFrame int
	Weapon     int   // active weapon slot (0=axe, 1=shotgun, ...)
	Ammo       [8]int
	InWater    bool
	Entities   []entities.EntityState
	Particles  []ParticleState
	Tracers    []TracerState
	Items      []ItemState // visible world items + live monsters + flames (combined)

	// Sound events accumulated during Tick; read by main.go, cleared at top of each Tick.
	SoundEvents []sound.SoundEvent
	SoundPaths  []string // path-keyed sounds (e.g. monster death sounds)

	// private weapon state
	weaponFrameTime  float32
	weaponSwinging   bool
	hitFired         bool
	weaponNumFrames  int
	hasWeapon        [8]bool
	weaponFrameCounts [8]int
	fireCooldown     float32
	mouseWasDown     bool

	// private physics state
	respawnPos mgl32.Vec3

	// private entity data
	monsters []entities.MonsterState
	flames   []entities.FlameState

	// private particle pool
	particles       [particleCount]particle
	particleScratch []ParticleState
	nextFreeHint    int

	// private tracer pool
	tracers       [tracerCount]tracer
	tracerScratch []TracerState

	// private item data
	itemSpawns []entities.ItemSpawn
	initItems  []ItemState
	picked     []bool

	// private BSP/entity refs
	m   *bsp.Map
	mgr *entities.Manager
	win *glfw.Window
}

// ammo caps per type (indexed by entities.AmmoShells..AmmoCells)
var ammoCaps = [8]int{0, 100, 200, 100, 100}

// New creates and initialises a Physics instance.
// initItems is a slice parallel to itemSpawns, pre-filled with Pos+MdlIdx for rendering.
func New(win *glfw.Window, m *bsp.Map, mgr *entities.Manager, spawn mgl32.Vec3,
	itemSpawns []entities.ItemSpawn, initItems []ItemState,
	monsters []entities.MonsterState, flames []entities.FlameState,
	weaponFrameCounts [8]int) *Physics {

	p := &Physics{
		Pos:               spawn,
		Health:            100,
		respawnPos:        spawn,
		weaponNumFrames:   weaponFrameCounts[0], // axe
		weaponFrameCounts: weaponFrameCounts,
		Weapon:            0,
		monsters:          monsters,
		flames:            flames,
		itemSpawns:        itemSpawns,
		initItems:         initItems,
		picked:            make([]bool, len(itemSpawns)),
		m:                 m,
		mgr:               mgr,
		win:               win,
	}
	p.hasWeapon[0] = true // axe always owned
	p.particleScratch = make([]ParticleState, 0, particleCount)
	p.tracerScratch = make([]TracerState, 0, tracerCount)
	p.LeafIndex = bsp.LeafForPoint(m, [3]float32(p.Pos))
	p.InWater = p.LeafIndex < len(m.Leaves) && m.Leaves[p.LeafIndex].Contents == bsp.ContentsWater
	return p
}

// Tick advances physics by dt seconds. Must be called from the GL/main thread.
func (p *Physics) Tick(dt float32) {
	p.SoundEvents = p.SoundEvents[:0]
	p.SoundPaths = p.SoundPaths[:0]
	snap := p.readInput()

	// Mouse look
	p.Yaw -= float32(snap.DX * mouseSens)
	p.Pitch -= float32(snap.DY * mouseSens)
	if p.Pitch > 89 {
		p.Pitch = 89
	}
	if p.Pitch < -89 {
		p.Pitch = -89
	}

	if dt <= 0 {
		p.buildItems()
		return
	}

	// No clip nodes → noclip fallback
	if len(p.m.ClipNodes) == 0 || len(p.m.Models) == 0 {
		noclip(p, snap, dt)
		return
	}

	hull := p.m.Models[0].HeadNodes[1] // standing player hull

	// Player origin = eye position minus view height
	origin := [3]float32{p.Pos[0], p.Pos[1], p.Pos[2] - eyeHeight}

	// Advance entity state machines before movement
	p.mgr.Update(dt, p.m, origin)

	yaw := float64(mgl32.DegToRad(p.Yaw))
	fwd := [3]float32{float32(math.Cos(yaw)), float32(math.Sin(yaw)), 0}
	right := [3]float32{float32(math.Sin(yaw)), float32(-math.Cos(yaw)), 0}

	// Desired horizontal velocity from WASD
	var wishX, wishY float32
	spd := float32(moveSpeed)
	if snap.Keys[glfw.KeyW] || snap.Keys[glfw.KeyUp] {
		wishX += fwd[0] * spd
		wishY += fwd[1] * spd
	}
	if snap.Keys[glfw.KeyS] || snap.Keys[glfw.KeyDown] {
		wishX -= fwd[0] * spd
		wishY -= fwd[1] * spd
	}
	if snap.Keys[glfw.KeyA] {
		wishX -= right[0] * spd
		wishY -= right[1] * spd
	}
	if snap.Keys[glfw.KeyD] {
		wishX += right[0] * spd
		wishY += right[1] * spd
	}
	// Override horizontal velocity — no momentum in Quake walking
	p.Velocity[0] = wishX
	p.Velocity[1] = wishY

	// Apply gravity when airborne
	if !p.OnGround {
		p.Velocity[2] -= gravity * dt
	}

	// Jump
	if snap.Keys[glfw.KeySpace] && p.OnGround {
		p.Velocity[2] = jumpSpeed
		p.OnGround = false
	}

	// Slide move
	disp := [3]float32{p.Velocity[0] * dt, p.Velocity[1] * dt, p.Velocity[2] * dt}
	newOrigin := slideMove(p.m, p.mgr.Entities, hull, origin, disp)

	// Ground check: trace 2 units down from new position
	groundEnd := [3]float32{newOrigin[0], newOrigin[1], newOrigin[2] - 2}
	gtr := traceAll(p.m, p.mgr.Entities, hull, newOrigin, groundEnd)
	if gtr.Hit && gtr.Normal[2] > 0.7 {
		p.OnGround = true
		p.Velocity[2] = 0
		newOrigin = gtr.EndPos
	} else {
		p.OnGround = false
	}

	p.Pos = mgl32.Vec3{newOrigin[0], newOrigin[1], newOrigin[2] + eyeHeight}
	p.LeafIndex = bsp.LeafForPoint(p.m, [3]float32(p.Pos))
	p.InWater = p.LeafIndex < len(p.m.Leaves) && p.m.Leaves[p.LeafIndex].Contents == bsp.ContentsWater
	p.Entities = p.mgr.States()

	// Weapon tick
	tickWeapon(p, snap, dt)

	// Monster tick (pass brush entities for door collision)
	tickMonsters(p, dt)

	// Particle + tracer tick
	tickParticles(p, dt)
	tickTracers(p, dt)
	tickFlames(p, dt)

	// Check item pickups against player foot origin.
	foot := [3]float32{p.Pos[0], p.Pos[1], p.Pos[2] - eyeHeight}
	for i, item := range p.itemSpawns {
		if p.picked[i] {
			continue
		}
		dx := foot[0] - item.Pos[0]
		dy := foot[1] - item.Pos[1]
		dz := foot[2] - item.Pos[2]
		if dx*dx+dy*dy+dz*dz < pickupRadius*pickupRadius {
			p.picked[i] = true
			p.SoundEvents = append(p.SoundEvents, sound.SndItemPickup)
			if item.HealthValue > 0 {
				p.Health += item.HealthValue
				if p.Health > 100 {
					p.Health = 100
				}
			}
			if item.WeaponType != entities.WeaponNone {
				p.hasWeapon[item.WeaponType] = true
				p.Weapon = item.WeaponType
				p.weaponNumFrames = p.weaponFrameCounts[item.WeaponType]
				p.WeaponFrame = 0
				p.weaponSwinging = false
				p.fireCooldown = 0
				p.mouseWasDown = false
				wt := item.WeaponType
				grantType := [8]int{0, entities.AmmoShells, entities.AmmoShells, entities.AmmoNails, entities.AmmoNails, entities.AmmoRockets, entities.AmmoRockets, entities.AmmoCells}
				grantAmt := [8]int{0, 25, 5, 30, 30, 5, 5, 15}
				at, amt := grantType[wt], grantAmt[wt]
				if at != entities.AmmoNone {
					p.Ammo[at] += amt
					if p.Ammo[at] > ammoCaps[at] {
						p.Ammo[at] = ammoCaps[at]
					}
				}
			}
			if item.AmmoType != entities.AmmoNone && item.AmmoAmount > 0 {
				p.Ammo[item.AmmoType] += item.AmmoAmount
				if item.AmmoType < len(ammoCaps) && p.Ammo[item.AmmoType] > ammoCaps[item.AmmoType] {
					p.Ammo[item.AmmoType] = ammoCaps[item.AmmoType]
				}
			}
		}
	}

	// Respawn on death
	if p.Health <= 0 {
		p.Pos = p.respawnPos
		p.Velocity = mgl32.Vec3{}
		p.OnGround = false
		p.Health = 100
		p.LeafIndex = bsp.LeafForPoint(p.m, [3]float32(p.Pos))
		p.InWater = p.LeafIndex < len(p.m.Leaves) && p.m.Leaves[p.LeafIndex].Contents == bsp.ContentsWater
		for i := range p.monsters {
			p.monsters[i].Alerted = false
		}
	}

	buildParticleItems(p)
	buildTracerItems(p)
	p.buildItems()
}

// buildItems builds p.Items from unpicked world items + live monsters + flames.
func (p *Physics) buildItems() {
	p.Items = p.Items[:0]
	for i, it := range p.initItems {
		if !p.picked[i] {
			p.Items = append(p.Items, it)
		}
	}
	for _, mn := range p.monsters {
		if mn.Dead {
			continue
		}
		p.Items = append(p.Items, ItemState{
			Pos:    mn.Pos,
			MdlIdx: mn.MdlIdx,
			Frame:  mn.FrameIdx,
			Yaw:    mn.Yaw,
		})
	}
	for _, f := range p.flames {
		p.Items = append(p.Items, ItemState{
			Pos:    f.Pos,
			MdlIdx: f.MdlIdx,
			Frame:  f.FrameIdx,
		})
	}
}

// readInput polls the GLFW window for current key/mouse state.
// Resets cursor to window centre and returns the delta.
func (p *Physics) readInput() inputSnapshot {
	var snap inputSnapshot
	for _, k := range []glfw.Key{
		glfw.KeyW, glfw.KeyA, glfw.KeyS, glfw.KeyD,
		glfw.KeyUp, glfw.KeyDown,
		glfw.KeySpace, glfw.KeyLeftControl, glfw.KeyC,
		glfw.Key1, glfw.Key2, glfw.Key3, glfw.Key4,
		glfw.Key5, glfw.Key6, glfw.Key7, glfw.Key8,
	} {
		if int(k) < 512 {
			snap.Keys[k] = p.win.GetKey(k) == glfw.Press
		}
	}
	mx, my := p.win.GetCursorPos()
	ww, wh := p.win.GetSize()
	cx, cy := float64(ww)/2, float64(wh)/2
	p.win.SetCursorPos(cx, cy)
	snap.DX = mx - cx
	snap.DY = my - cy
	for i := 0; i < 8; i++ {
		snap.MouseButtons[i] = p.win.GetMouseButton(glfw.MouseButton(i)) == glfw.Press
	}
	return snap
}

// weaponAmmoReq is the {ammoType, minCost} needed to fire each weapon slot.
var weaponAmmoReq = [8][2]int{
	{entities.AmmoNone, 0},    // 0 axe
	{entities.AmmoShells, 1},  // 1 shotgun
	{entities.AmmoShells, 2},  // 2 super shotgun
	{entities.AmmoNails, 1},   // 3 nailgun
	{entities.AmmoNails, 2},   // 4 super nailgun
	{entities.AmmoRockets, 1}, // 5 grenade launcher
	{entities.AmmoRockets, 1}, // 6 rocket launcher
	{entities.AmmoCells, 1},   // 7 lightning
}

// canFire reports whether the player has enough ammo to fire weapon slot w.
func canFire(p *Physics, w int) bool {
	at, cost := weaponAmmoReq[w][0], weaponAmmoReq[w][1]
	return at == entities.AmmoNone || p.Ammo[at] >= cost
}

// autoSwitchWeapon switches to the best owned weapon below the current one that can fire.
func autoSwitchWeapon(p *Physics) {
	for slot := p.Weapon - 1; slot >= 0; slot-- {
		if p.hasWeapon[slot] && canFire(p, slot) {
			p.Weapon = slot
			p.weaponNumFrames = p.weaponFrameCounts[slot]
			p.WeaponFrame = 0
			p.weaponSwinging = false
			p.fireCooldown = 0
			p.mouseWasDown = false
			return
		}
	}
}

// CurrentWeaponAmmo returns the current and maximum ammo for the active weapon.
// Both values are 0 for the axe (melee, no ammo).
func (p *Physics) CurrentWeaponAmmo() (current, max int) {
	if p.Weapon < 0 || p.Weapon >= len(weaponAmmoReq) {
		return 0, 0
	}
	at := weaponAmmoReq[p.Weapon][0]
	if at == entities.AmmoNone || at >= len(ammoCaps) {
		return 0, 0
	}
	return p.Ammo[at], ammoCaps[at]
}

// HasWeapon returns true if the player owns weapon slot w.
func (p *Physics) HasWeapon(w int) bool {
	if w < 0 || w >= len(p.hasWeapon) {
		return false
	}
	return p.hasWeapon[w]
}

// AmmoForSlot returns the current ammo count for the ammo type used by weapon slot w.
// Returns 0 for the axe (no ammo) or out-of-range slots.
func (p *Physics) AmmoForSlot(w int) int {
	if w < 0 || w >= len(weaponAmmoReq) {
		return 0
	}
	at := weaponAmmoReq[w][0]
	if at == entities.AmmoNone || at >= len(p.Ammo) {
		return 0
	}
	return p.Ammo[at]
}

// tickWeapon dispatches to the active weapon's tick and handles weapon switching.
func tickWeapon(p *Physics, snap inputSnapshot, dt float32) {
	// Weapon switching via keys 1–8.
	for slot := 0; slot < 8; slot++ {
		k := glfw.Key(int(glfw.Key1) + slot)
		if int(k) < 512 && snap.Keys[k] && p.hasWeapon[slot] && slot != p.Weapon {
			p.Weapon = slot
			p.weaponNumFrames = p.weaponFrameCounts[slot]
			p.WeaponFrame = 0
			p.weaponSwinging = false
			p.fireCooldown = 0
			p.mouseWasDown = false
		}
	}

	switch p.Weapon {
	case 0:
		tickAxe(p, snap, dt)
	case 1, 2:
		tickShotgun(p, snap, dt)
	case 3, 4:
		tickNailgun(p, snap, dt)
	case 5, 6:
		tickRocket(p, snap, dt)
	case 7:
		tickLightning(p, snap, dt)
	}

	// Auto-switch to lower-tier weapon when current weapon is out of ammo.
	if p.Weapon > 0 && !canFire(p, p.Weapon) {
		autoSwitchWeapon(p)
	}
}

// tickAxe advances the axe swing animation and fires melee hit detection.
func tickAxe(p *Physics, snap inputSnapshot, dt float32) {
	if p.weaponNumFrames <= 0 {
		return
	}

	// Start a swing on left-click when not already swinging.
	if snap.MouseButtons[0] && !p.weaponSwinging {
		p.weaponSwinging = true
		p.WeaponFrame = 1
		p.weaponFrameTime = 0
		p.hitFired = false
		p.SoundEvents = append(p.SoundEvents, sound.SndAxeSwing)
	}

	if !p.weaponSwinging {
		return
	}

	p.weaponFrameTime += dt * weaponFPS
	for p.weaponFrameTime >= 1.0 {
		p.weaponFrameTime -= 1.0
		p.WeaponFrame++

		// Fire hit check.
		if !p.hitFired && p.WeaponFrame >= weaponHitFrame {
			p.hitFired = true
			tryAxeHit(p)
		}

		// Swing completion.
		if p.WeaponFrame >= p.weaponNumFrames {
			p.WeaponFrame = 0
			p.weaponSwinging = false
			p.hitFired = false
			break
		}
	}
}

// tickShotgun handles semi-auto shotgun / super shotgun fire.
func tickShotgun(p *Physics, snap inputSnapshot, dt float32) {
	advanceRangedAnim(p, dt, false)
	mouseDown := snap.MouseButtons[0]
	if mouseDown && !p.mouseWasDown {
		cost := 1
		pellets := 6
		spread := float32(0.06)
		snd := sound.SndShotgun
		if p.Weapon == entities.WeaponSuperShotgun {
			cost = 2
			pellets = 14
			spread = 0.12
			snd = sound.SndSuperShotgun
		}
		if p.Ammo[entities.AmmoShells] >= cost {
			p.Ammo[entities.AmmoShells] -= cost
			fireHitscan(p, pellets, spread, 4)
			startRangedAnim(p)
			p.SoundEvents = append(p.SoundEvents, snd)
		}
	}
	p.mouseWasDown = mouseDown
}

// tickNailgun handles full-auto nailgun / super nailgun fire.
func tickNailgun(p *Physics, snap inputSnapshot, dt float32) {
	advanceRangedAnim(p, dt, true)
	if !snap.MouseButtons[0] {
		p.fireCooldown = 0
		p.weaponSwinging = false
		p.WeaponFrame = 0
		return
	}
	p.fireCooldown -= dt
	if p.fireCooldown > 0 {
		return
	}
	cost := 1
	damage := 9
	sndNail := sound.SndNailgun
	if p.Weapon == entities.WeaponSuperNailgun {
		cost = 2
		damage = 18
		sndNail = sound.SndSuperNailgun
	}
	if p.Ammo[entities.AmmoNails] >= cost {
		p.Ammo[entities.AmmoNails] -= cost
		fireHitscan(p, 1, 0, damage)
		p.fireCooldown = 0.1
		startRangedAnim(p)
		p.SoundEvents = append(p.SoundEvents, sndNail)
	}
}

// tickRocket handles semi-auto grenade / rocket launcher fire.
func tickRocket(p *Physics, snap inputSnapshot, dt float32) { //nolint
	advanceRangedAnim(p, dt, false)
	mouseDown := snap.MouseButtons[0]
	if mouseDown && !p.mouseWasDown {
		damage := 100
		sndRocket := sound.SndGrenade
		if p.Weapon == entities.WeaponRocketLauncher {
			damage = 120
			sndRocket = sound.SndRocket
		}
		if p.Ammo[entities.AmmoRockets] > 0 {
			p.Ammo[entities.AmmoRockets]--
			fireHitscan(p, 1, 0, damage)
			startRangedAnim(p)
			p.SoundEvents = append(p.SoundEvents, sndRocket)
		}
	}
	p.mouseWasDown = mouseDown
}

// tickLightning handles full-auto lightning gun fire.
func tickLightning(p *Physics, snap inputSnapshot, dt float32) {
	advanceRangedAnim(p, dt, true)
	if !snap.MouseButtons[0] {
		p.fireCooldown = 0
		p.weaponSwinging = false
		p.WeaponFrame = 0
		return
	}
	p.fireCooldown -= dt
	if p.fireCooldown > 0 {
		return
	}
	if p.Ammo[entities.AmmoCells] > 0 {
		p.Ammo[entities.AmmoCells]--
		fireHitscan(p, 1, 0, 30)
		p.fireCooldown = 0.05
		startRangedAnim(p)
		p.SoundEvents = append(p.SoundEvents, sound.SndLightning)
	}
}

// startRangedAnim begins the fire animation if not already playing.
func startRangedAnim(p *Physics) {
	if !p.weaponSwinging && p.weaponNumFrames > 1 {
		p.weaponSwinging = true
		p.WeaponFrame = 1
		p.weaponFrameTime = 0
	}
}

// advanceRangedAnim advances WeaponFrame for ranged weapons.
// loop=true: loops animation while weaponSwinging; loop=false: plays once then stops.
func advanceRangedAnim(p *Physics, dt float32, loop bool) {
	if !p.weaponSwinging || p.weaponNumFrames <= 1 {
		return
	}
	p.weaponFrameTime += dt * weaponFPS
	for p.weaponFrameTime >= 1.0 {
		p.weaponFrameTime -= 1.0
		p.WeaponFrame++
		if p.WeaponFrame >= p.weaponNumFrames {
			if loop {
				p.WeaponFrame = 1
			} else {
				p.WeaponFrame = 0
				p.weaponSwinging = false
				break
			}
		}
	}
}

// fireHitscan fires numPellets rays from the player eye with optional spread.
// Each pellet tests all live monsters using a ray-vs-sphere closest-point check,
// then traces the world geometry to emit wall sparks and a bullet tracer.
func fireHitscan(p *Physics, numPellets int, spreadFactor float32, damage int) {
	eyePos := [3]float32(p.Pos)
	yaw := float64(mgl32.DegToRad(p.Yaw))
	pitch := float64(mgl32.DegToRad(p.Pitch))
	baseFwdX := float32(math.Cos(pitch) * math.Cos(yaw))
	baseFwdY := float32(math.Cos(pitch) * math.Sin(yaw))
	baseFwdZ := float32(math.Sin(pitch))

	const rayLen = 2048.0
	const hitRadius = 40.0

	pointHull := p.m.Models[0].HeadNodes[0]

	for pellet := 0; pellet < numPellets; pellet++ {
		dx, dy, dz := baseFwdX, baseFwdY, baseFwdZ
		if spreadFactor > 0 && numPellets > 1 {
			dx += (rand.Float32()*2 - 1) * spreadFactor
			dy += (rand.Float32()*2 - 1) * spreadFactor
			dz += (rand.Float32()*2 - 1) * spreadFactor
			mag := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
			if mag < 1e-6 {
				mag = 1
			}
			dx /= mag
			dy /= mag
			dz /= mag
		}

		hitT := float32(rayLen)
		monsterHit := false
		var monsterHitPos [3]float32

		for i := range p.monsters {
			mn := &p.monsters[i]
			if mn.Dead {
				continue
			}
			// Closest point on ray to monster center.
			vx := mn.Pos[0] - eyePos[0]
			vy := mn.Pos[1] - eyePos[1]
			vz := mn.Pos[2] - eyePos[2]
			t := vx*dx + vy*dy + vz*dz
			if t < 0 {
				t = 0
			} else if t > rayLen {
				t = rayLen
			}
			cx := eyePos[0] + dx*t - mn.Pos[0]
			cy := eyePos[1] + dy*t - mn.Pos[1]
			cz := eyePos[2] + dz*t - mn.Pos[2]
			if cx*cx+cy*cy+cz*cz < hitRadius*hitRadius {
				mn.HP -= damage
				if mn.HP < 0 {
					mn.HP = 0
				}
				if t < hitT {
					hitT = t
					monsterHitPos = mn.Pos
				}
				monsterHit = true
			}
		}

		// World geometry trace up to the closest hit point.
		endPos := [3]float32{
			eyePos[0] + dx*hitT,
			eyePos[1] + dy*hitT,
			eyePos[2] + dz*hitT,
		}
		tr := bsp.HullTrace(p.m.ClipNodes, p.m.Planes, pointHull, eyePos, endPos)

		var hitPoint [3]float32
		if tr.Hit {
			hitPoint = tr.EndPos
			if !monsterHit {
				emitWallSparks(p, tr.EndPos, tr.Normal[0], tr.Normal[1], tr.Normal[2])
			}
		} else {
			hitPoint = endPos
		}

		if monsterHit {
			emitBloodParticles(p, monsterHitPos, dx, dy, dz)
		}

		emitTracer(p, muzzlePos(eyePos, baseFwdX, baseFwdY, baseFwdZ, yaw, pitch), hitPoint)
	}
}

// muzzlePos returns the approximate world-space weapon muzzle position.
// It mirrors the weapon render offset: Translate3D(0, -10, -10) in GL camera space
// = 10 units forward + 10 units down from the eye.
func muzzlePos(eye [3]float32, fwdX, fwdY, fwdZ float32, yaw, pitch float64) [3]float32 {
	// Up vector in world space (Z-up, pitched)
	upX := float32(-math.Sin(pitch) * math.Cos(yaw))
	upY := float32(-math.Sin(pitch) * math.Sin(yaw))
	upZ := float32(math.Cos(pitch))

	const fwdOff = 10.0
	const upOff = -10.0
	return [3]float32{
		eye[0] + fwdX*fwdOff + upX*upOff,
		eye[1] + fwdY*fwdOff + upY*upOff,
		eye[2] + fwdZ*fwdOff + upZ*upOff,
	}
}

// tryAxeHit casts a 64-unit ray from eye and sphere-tests against live monsters.
func tryAxeHit(p *Physics) {
	eyePos := [3]float32(p.Pos)
	yaw := float64(mgl32.DegToRad(p.Yaw))
	pitch := float64(mgl32.DegToRad(p.Pitch))
	fwdX := float32(math.Cos(pitch) * math.Cos(yaw))
	fwdY := float32(math.Cos(pitch) * math.Sin(yaw))
	fwdZ := float32(math.Sin(pitch))

	const rayLen = 64.0
	const hitRadius = 40.0
	tipX := eyePos[0] + fwdX*rayLen
	tipY := eyePos[1] + fwdY*rayLen
	tipZ := eyePos[2] + fwdZ*rayLen

	for i := range p.monsters {
		mn := &p.monsters[i]
		if mn.Dead {
			continue
		}
		dx := mn.Pos[0] - tipX
		dy := mn.Pos[1] - tipY
		dz := mn.Pos[2] - tipZ
		if dx*dx+dy*dy+dz*dz < hitRadius*hitRadius {
			mn.HP -= 25
			if mn.HP < 0 {
				mn.HP = 0
			}
			emitBloodParticles(p, mn.Pos, fwdX, fwdY, fwdZ)
			p.SoundEvents = append(p.SoundEvents, sound.SndAxeHit)
		}
	}
}

// tickMonsters advances monster AI, animation, gravity and collision for one tick.
func tickMonsters(p *Physics, dt float32) {
	if len(p.m.ClipNodes) == 0 || len(p.m.Models) == 0 {
		return
	}

	playerFoot := [3]float32{
		p.Pos[0],
		p.Pos[1],
		p.Pos[2] - eyeHeight,
	}
	pointHull := p.m.Models[0].HeadNodes[0]

	for i := range p.monsters {
		mn := &p.monsters[i]

		// Transition to death animation when HP first hits zero.
		if mn.HP <= 0 && mn.AnimState != entities.AnimDead && !mn.Dead {
			mn.AnimState = entities.AnimDead
			mn.FrameTime = 0
			if mn.DeadRange.Valid() {
				mn.FrameIdx = mn.DeadRange.Start
			} else {
				mn.Dead = true // no death animation available — remove immediately
			}
			if mn.DeathSoundPath != "" {
				p.SoundPaths = append(p.SoundPaths, mn.DeathSoundPath)
			}
		}

		if mn.Dead {
			continue
		}

		mn.FrameTime += dt * entities.MonsterFPS

		// Death animation plays once; hold on last frame then mark dead.
		if mn.AnimState == entities.AnimDead {
			if advanceOnce(&mn.FrameIdx, &mn.FrameTime, mn.DeadRange) {
				mn.Dead = true
			}
			continue
		}

		// Compute distance to player
		dx := playerFoot[0] - mn.Pos[0]
		dy := playerFoot[1] - mn.Pos[1]
		dz := playerFoot[2] - mn.Pos[2]
		dist := float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))

		// Trigger pain animation on incoming damage.
		if mn.HP < mn.PrevHP && mn.AnimState != entities.AnimPain {
			if mn.PainRange.Valid() {
				mn.AnimState = entities.AnimPain
				mn.FrameIdx = mn.PainRange.Start
				mn.FrameTime = 0
			}
		}
		mn.PrevHP = mn.HP

		// Advance pain animation; return to idle/run when it finishes.
		if mn.AnimState == entities.AnimPain {
			if advanceOnce(&mn.FrameIdx, &mn.FrameTime, mn.PainRange) {
				if mn.Alerted {
					mn.AnimState = entities.AnimRun
					mn.FrameIdx = mn.RunRange.Start
				} else {
					mn.AnimState = entities.AnimIdle
					mn.FrameIdx = mn.IdleRange.Start
				}
				mn.FrameTime = 0
			}
		} else {
			// Choose run vs idle based on chase state.
			desired := entities.AnimIdle
			if mn.Alerted {
				desired = entities.AnimRun
			}
			if desired != mn.AnimState {
				mn.AnimState = desired
				if desired == entities.AnimRun {
					mn.FrameIdx = mn.RunRange.Start
				} else {
					mn.FrameIdx = mn.IdleRange.Start
				}
				mn.FrameTime = 0
			}
			// Advance looping animation.
			if mn.AnimState == entities.AnimRun {
				advanceLoop(&mn.FrameIdx, &mn.FrameTime, mn.RunRange)
			} else {
				advanceLoop(&mn.FrameIdx, &mn.FrameTime, mn.IdleRange)
			}
		}

		// Alert check: LOS trace from monster to player (world only)
		if !mn.Alerted && dist < 1024 {
			tr := bsp.HullTrace(p.m.ClipNodes, p.m.Planes, pointHull, mn.Pos, playerFoot)
			if !tr.Hit {
				mn.Alerted = true
			}
		}

		if mn.Alerted && dist > entities.MonsterMeleeDist && dist > 0 {
			// Face and chase toward player.
			mn.Yaw = float32(math.Atan2(float64(dy), float64(dx)))
			spd := entities.MonsterSpeed * dt
			moveEnd := [3]float32{
				mn.Pos[0] + dx/dist*spd,
				mn.Pos[1] + dy/dist*spd,
				mn.Pos[2],
			}
			tr := monsterMoveTrace(p.m, p.mgr.Entities, mn.Pos, moveEnd)
			// Monster-to-monster separation: reject XY move if it would overlap another monster.
			newX, newY := tr.EndPos[0], tr.EndPos[1]
			overlap := false
			for j := range p.monsters {
				if i == j || p.monsters[j].Dead {
					continue
				}
				ddx := newX - p.monsters[j].Pos[0]
				ddy := newY - p.monsters[j].Pos[1]
				if ddx*ddx+ddy*ddy < monsterSepRadius*monsterSepRadius {
					overlap = true
					break
				}
			}
			if !overlap {
				mn.Pos[0] = tr.EndPos[0]
				mn.Pos[1] = tr.EndPos[1]
			}
		}

		// Gravity and ground snapping.
		liftedZ := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] + monsterHullOffset + 1}
		if mn.OnGround {
			groundEnd := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] - 8}
			gtr := monsterMoveTrace(p.m, p.mgr.Entities, liftedZ, groundEnd)
			if gtr.Hit && gtr.Normal[2] > 0.7 {
				mn.Pos[2] = gtr.EndPos[2]
				mn.VelZ = 0
			} else {
				mn.OnGround = false // walked off a ledge
			}
		} else {
			mn.VelZ -= gravity * dt
			zEnd := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] + mn.VelZ*dt}
			tr := monsterMoveTrace(p.m, p.mgr.Entities, liftedZ, zEnd)
			if tr.Hit {
				mn.Pos[2] = tr.EndPos[2]
				if tr.Normal[2] > 0.7 {
					mn.VelZ = 0
					mn.OnGround = true
				} else if tr.Normal[2] < -0.7 {
					mn.VelZ = 0 // hit ceiling
				}
			} else {
				mn.Pos[2] = zEnd[2]
			}
		}

		// Melee attack
		mn.AttackCooldown -= dt
		if mn.Alerted && dist < entities.MonsterMeleeDist && mn.AttackCooldown <= 0 {
			p.Health -= entities.MonsterDamage
			mn.AttackCooldown = entities.MonsterAttackCooldown
		}
	}
}

// emitBloodParticles sprays blood from origin toward the hit direction.
func emitBloodParticles(p *Physics, origin [3]float32, fwdX, fwdY, fwdZ float32) {
	emitted := 0
	for emitted < particleEmitCount {
		found := false
		for range p.particles {
			i := p.nextFreeHint % particleCount
			p.nextFreeHint++
			if !p.particles[i].Active {
				lx := fwdX + (rand.Float32()*2-1)*particleSpread
				ly := fwdY + (rand.Float32()*2-1)*particleSpread
				lz := fwdZ + (rand.Float32()*2-1)*particleSpread
				mag := float32(math.Sqrt(float64(lx*lx + ly*ly + lz*lz)))
				if mag < 1e-6 {
					mag = 1
				}
				speed := particleSpeed * (0.5 + rand.Float32()*0.5)
				p.particles[i] = particle{
					Pos:     origin,
					Vel:     [3]float32{lx / mag * speed, ly / mag * speed, lz / mag * speed},
					Life:    particleFlyLife,
					MaxLife: particleFlyLife,
					Active:  true,
					Stuck:   false,
					Kind:    particleKindBlood,
				}
				found = true
				break
			}
		}
		if !found {
			break // pool exhausted
		}
		emitted++
	}
}

// emitWallSparks sprays spark particles from a wall impact point in the hemisphere around the surface normal.
func emitWallSparks(p *Physics, origin [3]float32, nx, ny, nz float32) {
	emitted := 0
	for emitted < sparkEmitCount {
		found := false
		for range p.particles {
			i := p.nextFreeHint % particleCount
			p.nextFreeHint++
			if !p.particles[i].Active {
				lx := nx + (rand.Float32()*2-1)*sparkSpread
				ly := ny + (rand.Float32()*2-1)*sparkSpread
				lz := nz + (rand.Float32()*2-1)*sparkSpread
				mag := float32(math.Sqrt(float64(lx*lx + ly*ly + lz*lz)))
				if mag < 1e-6 {
					mag = 1
				}
				speed := sparkSpeed * (0.5 + rand.Float32()*0.5)
				p.particles[i] = particle{
					Pos:     origin,
					Vel:     [3]float32{lx / mag * speed, ly / mag * speed, lz / mag * speed},
					Life:    sparkFlyLife,
					MaxLife: sparkFlyLife,
					Active:  true,
					Stuck:   false,
					Kind:    particleKindSpark,
				}
				found = true
				break
			}
		}
		if !found {
			break
		}
		emitted++
	}
}

// emitTracer adds a bullet tracer line segment to the pool.
func emitTracer(p *Physics, from, to [3]float32) {
	for i := range p.tracers {
		if !p.tracers[i].Active {
			p.tracers[i] = tracer{
				From:   from,
				To:     to,
				Life:   tracerLifetime,
				Active: true,
			}
			return
		}
	}
}

// tickTracers decays all active tracers.
func tickTracers(p *Physics, dt float32) {
	for i := range p.tracers {
		if !p.tracers[i].Active {
			continue
		}
		p.tracers[i].Life -= dt
		if p.tracers[i].Life <= 0 {
			p.tracers[i].Active = false
		}
	}
}

// buildTracerItems collects active tracers into p.Tracers.
func buildTracerItems(p *Physics) {
	p.tracerScratch = p.tracerScratch[:0]
	for i := range p.tracers {
		tr := &p.tracers[i]
		if !tr.Active {
			continue
		}
		p.tracerScratch = append(p.tracerScratch, TracerState{
			From: tr.From,
			To:   tr.To,
			Life: tr.Life / tracerLifetime,
		})
	}
	p.Tracers = p.tracerScratch
}

// tickFlames advances flame animation frames (looping, no AI or physics).
func tickFlames(p *Physics, dt float32) {
	for i := range p.flames {
		f := &p.flames[i]
		f.FrameTime += dt * entities.MonsterFPS
		for f.FrameTime >= 1.0 {
			f.FrameTime -= 1.0
			f.FrameIdx++
			if f.FrameIdx >= f.NumFrames {
				f.FrameIdx = 0
			}
		}
	}
}

// tickParticles advances all active particles for one physics tick.
func tickParticles(p *Physics, dt float32) {
	if len(p.m.ClipNodes) == 0 || len(p.m.Models) == 0 {
		return
	}
	pointHull := p.m.Models[0].HeadNodes[0]
	for i := range p.particles {
		pt := &p.particles[i]
		if !pt.Active {
			continue
		}
		pt.Life -= dt
		if pt.Life <= 0 {
			pt.Active = false
			continue
		}
		if pt.Stuck {
			continue
		}
		// Apply gravity
		pt.Vel[2] -= gravity * dt
		end := [3]float32{
			pt.Pos[0] + pt.Vel[0]*dt,
			pt.Pos[1] + pt.Vel[1]*dt,
			pt.Pos[2] + pt.Vel[2]*dt,
		}
		stuckLife := float32(particleStuckLife)
		if pt.Kind == particleKindSpark {
			stuckLife = sparkStuckLife
		}
		tr := bsp.HullTrace(p.m.ClipNodes, p.m.Planes, pointHull, pt.Pos, end)
		if tr.StartSolid {
			pt.Stuck = true
			if pt.Life > stuckLife {
				pt.Life = stuckLife
				pt.MaxLife = pt.Life
			}
		} else if tr.Hit {
			pt.Pos = tr.EndPos
			pt.Vel = [3]float32{}
			pt.Stuck = true
			if pt.Life > stuckLife {
				pt.Life = stuckLife
				pt.MaxLife = pt.Life
			}
		} else {
			pt.Pos = end
		}
	}
}

// buildParticleItems collects active particles into p.Particles.
func buildParticleItems(p *Physics) {
	p.particleScratch = p.particleScratch[:0]
	for i := range p.particles {
		pt := &p.particles[i]
		if !pt.Active {
			continue
		}
		life := float32(1)
		if pt.MaxLife > 0 {
			life = pt.Life / pt.MaxLife
		}
		p.particleScratch = append(p.particleScratch, ParticleState{
			Pos:   pt.Pos,
			Life:  life,
			Stuck: pt.Stuck,
			Kind:  pt.Kind,
		})
	}
	p.Particles = p.particleScratch
}

// monsterMoveTrace traces a point through world + brush entities using the point hull.
func monsterMoveTrace(m *bsp.Map, brushEnts []*entities.BrushEntity, start, end [3]float32) bsp.TraceResult {
	pointHull := m.Models[0].HeadNodes[0]
	best := bsp.HullTrace(m.ClipNodes, m.Planes, pointHull, start, end)
	for _, ent := range brushEnts {
		entModel := m.Models[ent.ModelIndex]
		entHull := entModel.HeadNodes[1]
		mo := entModel.Origin
		adjStart := [3]float32{
			start[0] - mo[0] - ent.Offset[0],
			start[1] - mo[1] - ent.Offset[1],
			start[2] - mo[2] - ent.Offset[2],
		}
		adjEnd := [3]float32{
			end[0] - mo[0] - ent.Offset[0],
			end[1] - mo[1] - ent.Offset[1],
			end[2] - mo[2] - ent.Offset[2],
		}
		tr := bsp.HullTrace(m.ClipNodes, m.Planes, int32(entHull), adjStart, adjEnd)
		if tr.Hit && (!best.Hit || tr.Fraction < best.Fraction) {
			tr.EndPos = [3]float32{
				tr.EndPos[0] + mo[0] + ent.Offset[0],
				tr.EndPos[1] + mo[1] + ent.Offset[1],
				tr.EndPos[2] + mo[2] + ent.Offset[2],
			}
			best = tr
		}
	}
	return best
}

// advanceLoop advances frameIdx within the range, looping, consuming accumulated frameTime.
func advanceLoop(idx *int, ft *float32, r entities.AnimRange) {
	for *ft >= 1.0 {
		*ft -= 1.0
		*idx++
		if *idx > r.End {
			*idx = r.Start
		}
	}
}

// advanceOnce advances frameIdx toward r.End without looping, consuming accumulated frameTime.
// Returns true when the last frame has been reached.
func advanceOnce(idx *int, ft *float32, r entities.AnimRange) bool {
	for *ft >= 1.0 {
		*ft -= 1.0
		if *idx < r.End {
			*idx++
		} else {
			return true
		}
	}
	return false
}

// slideMove moves origin by disp, sliding along surfaces on impact (2-pass).
func slideMove(m *bsp.Map, ents []*entities.BrushEntity, hull int32, origin, disp [3]float32) [3]float32 {
	end := [3]float32{origin[0] + disp[0], origin[1] + disp[1], origin[2] + disp[2]}
	tr := traceAll(m, ents, hull, origin, end)
	if !tr.Hit {
		return end
	}
	if tr.StartSolid {
		return origin // stuck in solid, don't move
	}

	// Clip remaining displacement along the hit normal.
	rem := 1 - tr.Fraction
	d := disp[0]*tr.Normal[0] + disp[1]*tr.Normal[1] + disp[2]*tr.Normal[2]
	slide := [3]float32{
		(disp[0] - tr.Normal[0]*d) * rem,
		(disp[1] - tr.Normal[1]*d) * rem,
		(disp[2] - tr.Normal[2]*d) * rem,
	}

	// Second pass along the slide direction.
	end2 := [3]float32{tr.EndPos[0] + slide[0], tr.EndPos[1] + slide[1], tr.EndPos[2] + slide[2]}
	tr2 := traceAll(m, ents, hull, tr.EndPos, end2)
	if !tr2.Hit {
		return end2
	}
	return tr2.EndPos
}

// traceAll traces the segment start→end against the world hull and all entity hulls,
// returning the earliest hit.
func traceAll(m *bsp.Map, ents []*entities.BrushEntity, hull int32, start, end [3]float32) bsp.TraceResult {
	best := bsp.HullTrace(m.ClipNodes, m.Planes, hull, start, end)
	for _, ent := range ents {
		entModel := m.Models[ent.ModelIndex]
		entHull := entModel.HeadNodes[1]
		mo := entModel.Origin
		adjStart := [3]float32{
			start[0] - mo[0] - ent.Offset[0],
			start[1] - mo[1] - ent.Offset[1],
			start[2] - mo[2] - ent.Offset[2],
		}
		adjEnd := [3]float32{
			end[0] - mo[0] - ent.Offset[0],
			end[1] - mo[1] - ent.Offset[1],
			end[2] - mo[2] - ent.Offset[2],
		}
		tr := bsp.HullTrace(m.ClipNodes, m.Planes, int32(entHull), adjStart, adjEnd)
		if tr.Hit && (!best.Hit || tr.Fraction < best.Fraction) {
			tr.EndPos = [3]float32{
				tr.EndPos[0] + mo[0] + ent.Offset[0],
				tr.EndPos[1] + mo[1] + ent.Offset[1],
				tr.EndPos[2] + mo[2] + ent.Offset[2],
			}
			best = tr
		}
	}
	return best
}

// noclip is the fallback movement when no clip hull is available.
func noclip(p *Physics, snap inputSnapshot, dt float32) {
	yaw := float64(mgl32.DegToRad(p.Yaw))
	fwd := [3]float32{float32(math.Cos(yaw)), float32(math.Sin(yaw)), 0}
	right := [3]float32{float32(math.Sin(yaw)), float32(-math.Cos(yaw)), 0}
	spd := moveSpeed * dt
	move := mgl32.Vec3{}
	if snap.Keys[glfw.KeyW] || snap.Keys[glfw.KeyUp] {
		move = move.Add(mgl32.Vec3(fwd).Mul(spd))
	}
	if snap.Keys[glfw.KeyS] || snap.Keys[glfw.KeyDown] {
		move = move.Sub(mgl32.Vec3(fwd).Mul(spd))
	}
	if snap.Keys[glfw.KeyA] {
		move = move.Sub(mgl32.Vec3(right).Mul(spd))
	}
	if snap.Keys[glfw.KeyD] {
		move = move.Add(mgl32.Vec3(right).Mul(spd))
	}
	if snap.Keys[glfw.KeySpace] {
		move[2] += spd
	}
	if snap.Keys[glfw.KeyLeftControl] || snap.Keys[glfw.KeyC] {
		move[2] -= spd
	}
	p.Pos = p.Pos.Add(move)
	p.LeafIndex = bsp.LeafForPoint(p.m, [3]float32(p.Pos))
	p.InWater = p.LeafIndex < len(p.m.Leaves) && p.m.Leaves[p.LeafIndex].Contents == bsp.ContentsWater

	tickWeapon(p, snap, dt)
	tickMonsters(p, dt)
	tickParticles(p, dt)
	tickTracers(p, dt)
	tickFlames(p, dt)

	buildParticleItems(p)
	buildTracerItems(p)
	p.buildItems()
}
