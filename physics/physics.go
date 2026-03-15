package physics

import (
	"math"
	"math/rand"

	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/entities"
	"go-quake/game"
	"go-quake/vis"
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

	monsterSepRadius  = 28.0 // minimum XY distance between monster centers
	monsterHullOffset = 24   // hull 1 bottom extent below entity origin (Quake standing hull)
)

// particle is an internal blood particle, owned by the physics goroutine.
type particle struct {
	Pos, Vel [3]float32
	Life     float32
	MaxLife  float32
	Stuck    bool
	Active   bool
}

// physicsState holds all mutable physics state between ticks.
type physicsState struct {
	player          game.PlayerState
	playerHP        int
	respawnPos      mgl32.Vec3
	weaponFrame     int
	weaponFrameTime float32
	weaponSwinging  bool
	hitFired        bool
	weaponNumFrames int
	// multi-weapon state
	currentWeapon     int
	hasWeapon         [8]bool
	ammo              [8]int // index 1=shells, 2=nails, 3=rockets, 4=cells
	weaponFrameCounts [8]int
	fireCooldown      float32
	mouseWasDown      bool
	monsters          []entities.MonsterState
	flames            []entities.FlameState
	particles         [particleCount]particle
	particleScratch   []game.ParticleState // pre-alloc, reused each tick
	nextFreeHint      int                  // amortised free-slot cursor
}

// ammo caps per type (indexed by entities.AmmoShells..AmmoCells)
var ammoCaps = [8]int{0, 100, 200, 100, 100}

// Run is the physics goroutine. It receives input events and emits player states.
func Run(m *bsp.Map, mgr *entities.Manager, bus *game.Bus, spawn mgl32.Vec3,
	items []entities.ItemSpawn, monsters []entities.MonsterState,
	flames []entities.FlameState, weaponFrameCounts [8]int) {

	ps := &physicsState{
		player: game.PlayerState{
			Position: spawn,
			Health:   100,
		},
		playerHP:          100,
		respawnPos:        spawn,
		weaponNumFrames:   weaponFrameCounts[0], // axe
		weaponFrameCounts: weaponFrameCounts,
		currentWeapon:     0,
		monsters:          monsters,
		flames:            flames,
	}
	ps.hasWeapon[0] = true // axe always owned
	ps.particleScratch = make([]game.ParticleState, 0, particleCount)
	ps.player.LeafIndex = vis.LeafForPoint(m, [3]float32(ps.player.Position))

	picked := make([]bool, len(items))

	for {
		select {
		case <-bus.Shutdown:
			return
		case ev := <-bus.Input:
			tick(m, mgr, ps, ev)

			// Check item pickups against player foot origin.
			origin := [3]float32{
				ps.player.Position[0],
				ps.player.Position[1],
				ps.player.Position[2] - eyeHeight,
			}
			for i, item := range items {
				if picked[i] {
					continue
				}
				dx := origin[0] - item.Pos[0]
				dy := origin[1] - item.Pos[1]
				dz := origin[2] - item.Pos[2]
				if dx*dx+dy*dy+dz*dz < pickupRadius*pickupRadius {
					picked[i] = true
					if item.HealthValue > 0 {
						ps.playerHP += item.HealthValue
						if ps.playerHP > 100 {
							ps.playerHP = 100
						}
					}
					if item.WeaponType != entities.WeaponNone {
						ps.hasWeapon[item.WeaponType] = true
						ps.currentWeapon = item.WeaponType
						ps.weaponNumFrames = ps.weaponFrameCounts[item.WeaponType]
						ps.weaponFrame = 0
						ps.weaponSwinging = false
						ps.fireCooldown = 0
						ps.mouseWasDown = false
						// Grant Quake-standard starting ammo for this weapon.
						wt := item.WeaponType
						grantType := [8]int{0, entities.AmmoShells, entities.AmmoShells, entities.AmmoNails, entities.AmmoNails, entities.AmmoRockets, entities.AmmoRockets, entities.AmmoCells}
						grantAmt := [8]int{0, 25, 5, 30, 30, 5, 5, 15}
						at, amt := grantType[wt], grantAmt[wt]
						if at != entities.AmmoNone {
							ps.ammo[at] += amt
							if ps.ammo[at] > ammoCaps[at] {
								ps.ammo[at] = ammoCaps[at]
							}
						}
					}
					if item.AmmoType != entities.AmmoNone && item.AmmoAmount > 0 {
						ps.ammo[item.AmmoType] += item.AmmoAmount
						if item.AmmoType < len(ammoCaps) && ps.ammo[item.AmmoType] > ammoCaps[item.AmmoType] {
							ps.ammo[item.AmmoType] = ammoCaps[item.AmmoType]
						}
					}
					select {
					case bus.ItemPickups <- i:
					default:
					}
				}
			}

			// Non-blocking send to coordinator
			select {
			case bus.Physics <- ps.player:
			default:
				// Drain stale and replace
				select {
				case <-bus.Physics:
				default:
				}
				bus.Physics <- ps.player
			}
		}
	}
}

func tick(m *bsp.Map, mgr *entities.Manager, ps *physicsState, ev game.InputEvent) {
	// Mouse look
	ps.player.Yaw -= float32(ev.MouseDX * mouseSens)
	ps.player.Pitch -= float32(ev.MouseDY * mouseSens)
	if ps.player.Pitch > 89 {
		ps.player.Pitch = 89
	}
	if ps.player.Pitch < -89 {
		ps.player.Pitch = -89
	}

	dt := float32(ev.Dt)
	if dt <= 0 {
		return
	}

	// No clip nodes → noclip fallback
	if len(m.ClipNodes) == 0 || len(m.Models) == 0 {
		noclip(m, ps, ev, dt)
		return
	}

	hull := m.Models[0].HeadNodes[1] // standing player hull

	// Player origin = eye position minus view height
	origin := [3]float32{ps.player.Position[0], ps.player.Position[1], ps.player.Position[2] - eyeHeight}

	// Advance entity state machines before movement
	mgr.Update(dt, m, origin)

	yaw := float64(mgl32.DegToRad(ps.player.Yaw))
	fwd := [3]float32{float32(math.Cos(yaw)), float32(math.Sin(yaw)), 0}
	right := [3]float32{float32(math.Sin(yaw)), float32(-math.Cos(yaw)), 0}

	// Desired horizontal velocity from WASD
	var wishX, wishY float32
	spd := float32(moveSpeed)
	if ev.Keys[glfw.KeyW] || ev.Keys[glfw.KeyUp] {
		wishX += fwd[0] * spd
		wishY += fwd[1] * spd
	}
	if ev.Keys[glfw.KeyS] || ev.Keys[glfw.KeyDown] {
		wishX -= fwd[0] * spd
		wishY -= fwd[1] * spd
	}
	if ev.Keys[glfw.KeyA] {
		wishX -= right[0] * spd
		wishY -= right[1] * spd
	}
	if ev.Keys[glfw.KeyD] {
		wishX += right[0] * spd
		wishY += right[1] * spd
	}
	// Override horizontal velocity — no momentum in Quake walking
	ps.player.Velocity[0] = wishX
	ps.player.Velocity[1] = wishY

	// Apply gravity when airborne
	if !ps.player.OnGround {
		ps.player.Velocity[2] -= gravity * dt
	}

	// Jump
	if ev.Keys[glfw.KeySpace] && ps.player.OnGround {
		ps.player.Velocity[2] = jumpSpeed
		ps.player.OnGround = false
	}

	// Slide move
	disp := [3]float32{ps.player.Velocity[0] * dt, ps.player.Velocity[1] * dt, ps.player.Velocity[2] * dt}
	newOrigin := slideMove(m, mgr.Entities, hull, origin, disp)

	// Ground check: trace 2 units down from new position
	groundEnd := [3]float32{newOrigin[0], newOrigin[1], newOrigin[2] - 2}
	gtr := traceAll(m, mgr.Entities, hull, newOrigin, groundEnd)
	if gtr.Hit && gtr.Normal[2] > 0.7 {
		ps.player.OnGround = true
		ps.player.Velocity[2] = 0
		newOrigin = gtr.EndPos
	} else {
		ps.player.OnGround = false
	}

	ps.player.Position = mgl32.Vec3{newOrigin[0], newOrigin[1], newOrigin[2] + eyeHeight}
	ps.player.LeafIndex = vis.LeafForPoint(m, [3]float32(ps.player.Position))
	ps.player.Entities = mgr.States()

	// Weapon tick
	tickWeapon(ps, ev, dt)

	// Monster tick (pass brush entities for door collision)
	tickMonsters(m, mgr.Entities, ps, dt)

	// Particle tick
	tickParticles(m, ps, dt)
	tickFlames(ps, dt)

	// Respawn on death
	if ps.playerHP <= 0 {
		ps.player.Position = ps.respawnPos
		ps.player.Velocity = mgl32.Vec3{}
		ps.player.OnGround = false
		ps.playerHP = 100
		ps.player.LeafIndex = vis.LeafForPoint(m, [3]float32(ps.player.Position))
		for i := range ps.monsters {
			ps.monsters[i].Alerted = false
		}
	}

	// Publish game state to player state
	ps.player.Health = ps.playerHP
	ps.player.WeaponFrame = ps.weaponFrame
	ps.player.CurrentWeapon = ps.currentWeapon
	ps.player.WeaponAmmo = ps.ammo

	// Build MonsterItems from live monsters
	ps.player.MonsterItems = ps.player.MonsterItems[:0]
	for _, mn := range ps.monsters {
		if mn.Dead {
			continue
		}
		ps.player.MonsterItems = append(ps.player.MonsterItems, game.ItemState{
			Pos:    mn.Pos,
			MdlIdx: mn.MdlIdx,
			Frame:  mn.FrameIdx,
			Yaw:    mn.Yaw,
		})
	}
	for _, f := range ps.flames {
		ps.player.MonsterItems = append(ps.player.MonsterItems, game.ItemState{
			Pos:    f.Pos,
			MdlIdx: f.MdlIdx,
			Frame:  f.FrameIdx,
		})
	}

	buildParticleItems(ps)
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
func canFire(ps *physicsState, w int) bool {
	at, cost := weaponAmmoReq[w][0], weaponAmmoReq[w][1]
	return at == entities.AmmoNone || ps.ammo[at] >= cost
}

// autoSwitchWeapon switches to the best owned weapon below the current one that can fire.
func autoSwitchWeapon(ps *physicsState) {
	for slot := ps.currentWeapon - 1; slot >= 0; slot-- {
		if ps.hasWeapon[slot] && canFire(ps, slot) {
			ps.currentWeapon = slot
			ps.weaponNumFrames = ps.weaponFrameCounts[slot]
			ps.weaponFrame = 0
			ps.weaponSwinging = false
			ps.fireCooldown = 0
			ps.mouseWasDown = false
			return
		}
	}
}

// tickWeapon dispatches to the active weapon's tick and handles weapon switching.
func tickWeapon(ps *physicsState, ev game.InputEvent, dt float32) {
	// Weapon switching via keys 1–8.
	for slot := 0; slot < 8; slot++ {
		k := glfw.Key(int(glfw.Key1) + slot)
		if int(k) < 512 && ev.Keys[k] && ps.hasWeapon[slot] && slot != ps.currentWeapon {
			ps.currentWeapon = slot
			ps.weaponNumFrames = ps.weaponFrameCounts[slot]
			ps.weaponFrame = 0
			ps.weaponSwinging = false
			ps.fireCooldown = 0
			ps.mouseWasDown = false
		}
	}

	switch ps.currentWeapon {
	case 0:
		tickAxe(ps, ev, dt)
	case 1, 2:
		tickShotgun(ps, ev, dt)
	case 3, 4:
		tickNailgun(ps, ev, dt)
	case 5, 6:
		tickRocket(ps, ev, dt)
	case 7:
		tickLightning(ps, ev, dt)
	}

	// Auto-switch to lower-tier weapon when current weapon is out of ammo.
	if ps.currentWeapon > 0 && !canFire(ps, ps.currentWeapon) {
		autoSwitchWeapon(ps)
	}
}

// tickAxe advances the axe swing animation and fires melee hit detection.
func tickAxe(ps *physicsState, ev game.InputEvent, dt float32) {
	if ps.weaponNumFrames <= 0 {
		return
	}

	// Start a swing on left-click when not already swinging.
	if ev.MouseButtons[0] && !ps.weaponSwinging {
		ps.weaponSwinging = true
		ps.weaponFrame = 1
		ps.weaponFrameTime = 0
		ps.hitFired = false
	}

	if !ps.weaponSwinging {
		return
	}

	ps.weaponFrameTime += dt * weaponFPS
	for ps.weaponFrameTime >= 1.0 {
		ps.weaponFrameTime -= 1.0
		ps.weaponFrame++

		// Fire hit check.
		if !ps.hitFired && ps.weaponFrame >= weaponHitFrame {
			ps.hitFired = true
			tryAxeHit(ps)
		}

		// Swing completion.
		if ps.weaponFrame >= ps.weaponNumFrames {
			ps.weaponFrame = 0
			ps.weaponSwinging = false
			ps.hitFired = false
			break
		}
	}
}

// tickShotgun handles semi-auto shotgun / super shotgun fire.
func tickShotgun(ps *physicsState, ev game.InputEvent, dt float32) {
	advanceRangedAnim(ps, dt, false)
	mouseDown := ev.MouseButtons[0]
	if mouseDown && !ps.mouseWasDown {
		cost := 1
		pellets := 6
		spread := float32(0.06)
		if ps.currentWeapon == entities.WeaponSuperShotgun {
			cost = 2
			pellets = 14
			spread = 0.12
		}
		if ps.ammo[entities.AmmoShells] >= cost {
			ps.ammo[entities.AmmoShells] -= cost
			fireHitscan(ps, pellets, spread, 4)
			startRangedAnim(ps)
		}
	}
	ps.mouseWasDown = mouseDown
}

// tickNailgun handles full-auto nailgun / super nailgun fire.
func tickNailgun(ps *physicsState, ev game.InputEvent, dt float32) {
	advanceRangedAnim(ps, dt, true)
	if !ev.MouseButtons[0] {
		ps.fireCooldown = 0
		ps.weaponSwinging = false
		ps.weaponFrame = 0
		return
	}
	ps.fireCooldown -= dt
	if ps.fireCooldown > 0 {
		return
	}
	cost := 1
	damage := 9
	if ps.currentWeapon == entities.WeaponSuperNailgun {
		cost = 2
		damage = 18
	}
	if ps.ammo[entities.AmmoNails] >= cost {
		ps.ammo[entities.AmmoNails] -= cost
		fireHitscan(ps, 1, 0, damage)
		ps.fireCooldown = 0.1
		startRangedAnim(ps)
	}
}

// tickRocket handles semi-auto grenade / rocket launcher fire.
func tickRocket(ps *physicsState, ev game.InputEvent, dt float32) { //nolint
	advanceRangedAnim(ps, dt, false)
	mouseDown := ev.MouseButtons[0]
	if mouseDown && !ps.mouseWasDown {
		damage := 100
		if ps.currentWeapon == entities.WeaponRocketLauncher {
			damage = 120
		}
		if ps.ammo[entities.AmmoRockets] > 0 {
			ps.ammo[entities.AmmoRockets]--
			fireHitscan(ps, 1, 0, damage)
			startRangedAnim(ps)
		}
	}
	ps.mouseWasDown = mouseDown
}

// tickLightning handles full-auto lightning gun fire.
func tickLightning(ps *physicsState, ev game.InputEvent, dt float32) {
	advanceRangedAnim(ps, dt, true)
	if !ev.MouseButtons[0] {
		ps.fireCooldown = 0
		ps.weaponSwinging = false
		ps.weaponFrame = 0
		return
	}
	ps.fireCooldown -= dt
	if ps.fireCooldown > 0 {
		return
	}
	if ps.ammo[entities.AmmoCells] > 0 {
		ps.ammo[entities.AmmoCells]--
		fireHitscan(ps, 1, 0, 30)
		ps.fireCooldown = 0.05
		startRangedAnim(ps)
	}
}

// startRangedAnim begins the fire animation if not already playing.
func startRangedAnim(ps *physicsState) {
	if !ps.weaponSwinging && ps.weaponNumFrames > 1 {
		ps.weaponSwinging = true
		ps.weaponFrame = 1
		ps.weaponFrameTime = 0
	}
}

// advanceRangedAnim advances weaponFrame for ranged weapons.
// loop=true: loops animation while weaponSwinging; loop=false: plays once then stops.
func advanceRangedAnim(ps *physicsState, dt float32, loop bool) {
	if !ps.weaponSwinging || ps.weaponNumFrames <= 1 {
		return
	}
	ps.weaponFrameTime += dt * weaponFPS
	for ps.weaponFrameTime >= 1.0 {
		ps.weaponFrameTime -= 1.0
		ps.weaponFrame++
		if ps.weaponFrame >= ps.weaponNumFrames {
			if loop {
				ps.weaponFrame = 1
			} else {
				ps.weaponFrame = 0
				ps.weaponSwinging = false
				break
			}
		}
	}
}

// fireHitscan fires numPellets rays from the player eye with optional spread.
// Each pellet tests all live monsters using a ray-vs-sphere closest-point check.
func fireHitscan(ps *physicsState, numPellets int, spreadFactor float32, damage int) {
	eyePos := [3]float32(ps.player.Position)
	yaw := float64(mgl32.DegToRad(ps.player.Yaw))
	pitch := float64(mgl32.DegToRad(ps.player.Pitch))
	baseFwdX := float32(math.Cos(pitch) * math.Cos(yaw))
	baseFwdY := float32(math.Cos(pitch) * math.Sin(yaw))
	baseFwdZ := float32(math.Sin(pitch))

	const rayLen = 2048.0
	const hitRadius = 40.0

	for p := 0; p < numPellets; p++ {
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

		for i := range ps.monsters {
			mn := &ps.monsters[i]
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
				emitBloodParticles(ps, mn.Pos, dx, dy, dz)
			}
		}
	}
}

// tryAxeHit casts a 64-unit ray from eye and sphere-tests against live monsters.
func tryAxeHit(ps *physicsState) {
	eyePos := [3]float32(ps.player.Position)
	yaw := float64(mgl32.DegToRad(ps.player.Yaw))
	pitch := float64(mgl32.DegToRad(ps.player.Pitch))
	fwdX := float32(math.Cos(pitch) * math.Cos(yaw))
	fwdY := float32(math.Cos(pitch) * math.Sin(yaw))
	fwdZ := float32(math.Sin(pitch))

	const rayLen = 64.0
	const hitRadius = 40.0
	tipX := eyePos[0] + fwdX*rayLen
	tipY := eyePos[1] + fwdY*rayLen
	tipZ := eyePos[2] + fwdZ*rayLen

	for i := range ps.monsters {
		mn := &ps.monsters[i]
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
			emitBloodParticles(ps, mn.Pos, fwdX, fwdY, fwdZ)
		}
	}
}

// tickMonsters advances monster AI, animation, gravity and collision for one tick.
func tickMonsters(m *bsp.Map, brushEnts []*entities.BrushEntity, ps *physicsState, dt float32) {
	if len(m.ClipNodes) == 0 || len(m.Models) == 0 {
		return
	}

	playerFoot := [3]float32{
		ps.player.Position[0],
		ps.player.Position[1],
		ps.player.Position[2] - eyeHeight,
	}
	pointHull := m.Models[0].HeadNodes[0]

	for i := range ps.monsters {
		mn := &ps.monsters[i]

		// Transition to death animation when HP first hits zero.
		if mn.HP <= 0 && mn.AnimState != entities.AnimDead && !mn.Dead {
			mn.AnimState = entities.AnimDead
			mn.FrameTime = 0
			if mn.DeadRange.Valid() {
				mn.FrameIdx = mn.DeadRange.Start
			} else {
				mn.Dead = true // no death animation available — remove immediately
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
			tr := bsp.HullTrace(m.ClipNodes, m.Planes, pointHull, mn.Pos, playerFoot)
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
			tr := monsterMoveTrace(m, brushEnts, mn.Pos, moveEnd)
			// Monster-to-monster separation: reject XY move if it would overlap another monster.
			newX, newY := tr.EndPos[0], tr.EndPos[1]
			overlap := false
			for j := range ps.monsters {
				if i == j || ps.monsters[j].Dead {
					continue
				}
				ddx := newX - ps.monsters[j].Pos[0]
				ddy := newY - ps.monsters[j].Pos[1]
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
		//
		// monsterMoveTrace uses m.ClipNodes with HeadNodes[0]==0, which is the root
		// of hull 1 (the player standing hull, 32×32×56). Hull 1 pre-expands solid
		// surfaces 24 units upward (|mins[2]|), so the solid boundary in clip-node
		// space sits at actual_floor_z + monsterHullOffset. Consequences:
		//   • Traces starting below this boundary (e.g. monster origin at floor_z)
		//     are StartSolid → Hit=false → gravity and wall detection break entirely.
		//   • The correct physics resting position is floor_z + monsterHullOffset,
		//     exactly where Quake's SV_DropToFloor places entity origins. MDL meshes
		//     have their feet at approx z = -monsterHullOffset in model space, so
		//     rendering at this origin puts feet visually at floor_z.
		//
		// Fix: always start vertical traces monsterHullOffset+1 above mn.Pos so the
		// origin is safely above the expanded solid boundary. Do NOT subtract the
		// offset from EndPos — mn.Pos should stay at floor_z+24 as the physics origin.
		liftedZ := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] + monsterHullOffset + 1}
		if mn.OnGround {
			// Step-down: keep the monster glued to sloped or stepped floors.
			groundEnd := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] - 8}
			gtr := monsterMoveTrace(m, brushEnts, liftedZ, groundEnd)
			if gtr.Hit && gtr.Normal[2] > 0.7 {
				mn.Pos[2] = gtr.EndPos[2]
				mn.VelZ = 0
			} else {
				mn.OnGround = false // walked off a ledge
			}
		} else {
			mn.VelZ -= gravity * dt
			zEnd := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] + mn.VelZ*dt}
			tr := monsterMoveTrace(m, brushEnts, liftedZ, zEnd)
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
			ps.playerHP -= entities.MonsterDamage
			mn.AttackCooldown = entities.MonsterAttackCooldown
		}
	}
}

// emitBloodParticles sprays blood from origin toward the hit direction.
func emitBloodParticles(ps *physicsState, origin [3]float32, fwdX, fwdY, fwdZ float32) {
	emitted := 0
	for emitted < particleEmitCount {
		// Find a free slot starting from the hint cursor.
		found := false
		for range ps.particles {
			i := ps.nextFreeHint % particleCount
			ps.nextFreeHint++
			if !ps.particles[i].Active {
				// Random cone spread around the forward vector.
				lx := fwdX + (rand.Float32()*2-1)*particleSpread
				ly := fwdY + (rand.Float32()*2-1)*particleSpread
				lz := fwdZ + (rand.Float32()*2-1)*particleSpread
				// Normalise
				mag := float32(math.Sqrt(float64(lx*lx + ly*ly + lz*lz)))
				if mag < 1e-6 {
					mag = 1
				}
				speed := particleSpeed * (0.5 + rand.Float32()*0.5)
				ps.particles[i] = particle{
					Pos:     origin,
					Vel:     [3]float32{lx / mag * speed, ly / mag * speed, lz / mag * speed},
					Life:    particleFlyLife,
					MaxLife: particleFlyLife,
					Active:  true,
					Stuck:   false,
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

// tickFlames advances flame animation frames (looping, no AI or physics).
func tickFlames(ps *physicsState, dt float32) {
	for i := range ps.flames {
		f := &ps.flames[i]
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
func tickParticles(m *bsp.Map, ps *physicsState, dt float32) {
	if len(m.ClipNodes) == 0 || len(m.Models) == 0 {
		return
	}
	pointHull := m.Models[0].HeadNodes[0]
	for i := range ps.particles {
		p := &ps.particles[i]
		if !p.Active {
			continue
		}
		p.Life -= dt
		if p.Life <= 0 {
			p.Active = false
			continue
		}
		if p.Stuck {
			continue
		}
		// Apply gravity
		p.Vel[2] -= gravity * dt
		end := [3]float32{
			p.Pos[0] + p.Vel[0]*dt,
			p.Pos[1] + p.Vel[1]*dt,
			p.Pos[2] + p.Vel[2]*dt,
		}
		tr := bsp.HullTrace(m.ClipNodes, m.Planes, pointHull, p.Pos, end)
		if tr.StartSolid {
			p.Stuck = true
			if p.Life > particleStuckLife {
				p.Life = particleStuckLife
				p.MaxLife = p.Life
			}
		} else if tr.Hit {
			p.Pos = tr.EndPos
			p.Vel = [3]float32{}
			p.Stuck = true
			if p.Life > particleStuckLife {
				p.Life = particleStuckLife
				p.MaxLife = p.Life
			}
		} else {
			p.Pos = end
		}
	}
}

// buildParticleItems collects active particles into ps.particleScratch.
func buildParticleItems(ps *physicsState) {
	ps.particleScratch = ps.particleScratch[:0]
	for i := range ps.particles {
		p := &ps.particles[i]
		if !p.Active {
			continue
		}
		life := float32(1)
		if p.MaxLife > 0 {
			life = p.Life / p.MaxLife
		}
		ps.particleScratch = append(ps.particleScratch, game.ParticleState{
			Pos:   p.Pos,
			Life:  life,
			Stuck: p.Stuck,
		})
	}
	ps.player.Particles = ps.particleScratch
}

// monsterMoveTrace traces a point through world + brush entities using the point hull.
func monsterMoveTrace(m *bsp.Map, brushEnts []*entities.BrushEntity, start, end [3]float32) bsp.TraceResult {
	pointHull := m.Models[0].HeadNodes[0]
	best := bsp.HullTrace(m.ClipNodes, m.Planes, pointHull, start, end)
	for _, ent := range brushEnts {
		entModel := m.Models[ent.ModelIndex]
		entHull := entModel.HeadNodes[1] // HeadNodes[1] = hull-1 clip root; HeadNodes[0] is the render-BSP root (wrong array)
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
		// Adjust trace into entity local space (entity origin + current offset)
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
			// Translate end position back to world space
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
func noclip(m *bsp.Map, ps *physicsState, ev game.InputEvent, dt float32) {
	yaw := float64(mgl32.DegToRad(ps.player.Yaw))
	fwd := [3]float32{float32(math.Cos(yaw)), float32(math.Sin(yaw)), 0}
	right := [3]float32{float32(math.Sin(yaw)), float32(-math.Cos(yaw)), 0}
	spd := moveSpeed * dt
	move := mgl32.Vec3{}
	if ev.Keys[glfw.KeyW] || ev.Keys[glfw.KeyUp] {
		move = move.Add(mgl32.Vec3(fwd).Mul(spd))
	}
	if ev.Keys[glfw.KeyS] || ev.Keys[glfw.KeyDown] {
		move = move.Sub(mgl32.Vec3(fwd).Mul(spd))
	}
	if ev.Keys[glfw.KeyA] {
		move = move.Sub(mgl32.Vec3(right).Mul(spd))
	}
	if ev.Keys[glfw.KeyD] {
		move = move.Add(mgl32.Vec3(right).Mul(spd))
	}
	if ev.Keys[glfw.KeySpace] {
		move[2] += spd
	}
	if ev.Keys[glfw.KeyLeftControl] || ev.Keys[glfw.KeyC] {
		move[2] -= spd
	}
	ps.player.Position = ps.player.Position.Add(move)
	ps.player.LeafIndex = vis.LeafForPoint(m, [3]float32(ps.player.Position))

	tickWeapon(ps, ev, dt)
	tickMonsters(m, nil, ps, dt)
	tickParticles(m, ps, dt)
	tickFlames(ps, dt)

	ps.player.Health = ps.playerHP
	ps.player.WeaponFrame = ps.weaponFrame
	ps.player.CurrentWeapon = ps.currentWeapon
	ps.player.WeaponAmmo = ps.ammo
	ps.player.MonsterItems = ps.player.MonsterItems[:0]
	for _, mn := range ps.monsters {
		if mn.Dead {
			continue
		}
		ps.player.MonsterItems = append(ps.player.MonsterItems, game.ItemState{
			Pos:    mn.Pos,
			MdlIdx: mn.MdlIdx,
			Frame:  mn.FrameIdx,
			Yaw:    mn.Yaw,
		})
	}
	for _, f := range ps.flames {
		ps.player.MonsterItems = append(ps.player.MonsterItems, game.ItemState{
			Pos:    f.Pos,
			MdlIdx: f.MdlIdx,
			Frame:  f.FrameIdx,
		})
	}

	buildParticleItems(ps)
}
