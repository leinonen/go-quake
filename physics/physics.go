package physics

import (
	"math"

	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/entities"
	"go-quake/game"
	"go-quake/vis"
)

const (
	mouseSens    = 0.15
	moveSpeed    = 320.0 // units/s, classic Quake speed
	eyeHeight    = 22.0  // view height above player origin (Quake DEFAULT_VIEWHEIGHT)
	gravity      = 800.0 // units/s^2
	jumpSpeed    = 270.0 // initial Z velocity on jump
	pickupRadius = 32.0  // item touch radius (Quake standard)
	weaponFPS    = 8.0   // weapon animation frames per second
	weaponHitFrame = 2   // axe MDL frame index at which hit detection fires
)

// physicsState holds all mutable physics state between ticks.
type physicsState struct {
	player         game.PlayerState
	playerHP       int
	respawnPos     mgl32.Vec3
	weaponFrame    int
	weaponFrameTime float32
	weaponSwinging  bool
	hitFired        bool
	weaponNumFrames int
	monsters        []entities.MonsterState
}

// Run is the physics goroutine. It receives input events and emits player states.
func Run(m *bsp.Map, mgr *entities.Manager, bus *game.Bus, spawn mgl32.Vec3,
	items []entities.ItemSpawn, monsters []entities.MonsterState, numWeaponFrames int) {

	ps := &physicsState{
		player: game.PlayerState{
			Position: spawn,
			Health:   100,
		},
		playerHP:        100,
		respawnPos:      spawn,
		weaponNumFrames: numWeaponFrames,
		monsters:        monsters,
	}
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
}

// tickWeapon advances axe animation and fires hit detection.
func tickWeapon(ps *physicsState, ev game.InputEvent, dt float32) {
	if ps.weaponNumFrames <= 0 {
		return
	}

	// Start a swing on left-click when not already swinging
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

		// Fire hit check
		if !ps.hitFired && ps.weaponFrame >= weaponHitFrame {
			ps.hitFired = true
			tryAxeHit(ps)
		}

		// Swing completion
		if ps.weaponFrame >= ps.weaponNumFrames {
			ps.weaponFrame = 0
			ps.weaponSwinging = false
			ps.hitFired = false
			break
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
			mn.Pos[0] = tr.EndPos[0]
			mn.Pos[1] = tr.EndPos[1]
		}

		// Gravity: accumulate downward velocity and move Z
		mn.VelZ -= gravity * dt
		zEnd := [3]float32{mn.Pos[0], mn.Pos[1], mn.Pos[2] + mn.VelZ*dt}
		tr := monsterMoveTrace(m, brushEnts, mn.Pos, zEnd)
		mn.Pos[2] = tr.EndPos[2]
		if tr.Hit {
			if tr.Normal[2] > 0.7 || tr.Normal[2] < -0.7 {
				mn.VelZ = 0
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

// monsterMoveTrace traces a point through world + brush entities using the point hull.
func monsterMoveTrace(m *bsp.Map, brushEnts []*entities.BrushEntity, start, end [3]float32) bsp.TraceResult {
	pointHull := m.Models[0].HeadNodes[0]
	best := bsp.HullTrace(m.ClipNodes, m.Planes, pointHull, start, end)
	for _, ent := range brushEnts {
		entModel := m.Models[ent.ModelIndex]
		entHull := entModel.HeadNodes[0]
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

	ps.player.Health = ps.playerHP
	ps.player.WeaponFrame = ps.weaponFrame
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
}
