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
	mouseSens = 0.15
	moveSpeed = 320.0 // units/s, classic Quake speed
	eyeHeight = 22.0  // view height above player origin (Quake DEFAULT_VIEWHEIGHT)
	gravity   = 800.0 // units/s^2
	jumpSpeed = 270.0 // initial Z velocity on jump
)

// Run is the physics goroutine. It receives input events and emits player states.
func Run(m *bsp.Map, mgr *entities.Manager, bus *game.Bus, spawn mgl32.Vec3) {
	state := game.PlayerState{
		Position: spawn,
		Yaw:      0,
		Pitch:    0,
	}
	state.LeafIndex = vis.LeafForPoint(m, [3]float32(state.Position))

	for {
		select {
		case <-bus.Shutdown:
			return
		case ev := <-bus.Input:
			state = tick(m, mgr, state, ev)
			// Non-blocking send to coordinator
			select {
			case bus.Physics <- state:
			default:
				// Drain stale and replace
				select {
				case <-bus.Physics:
				default:
				}
				bus.Physics <- state
			}
		}
	}
}

func tick(m *bsp.Map, mgr *entities.Manager, s game.PlayerState, ev game.InputEvent) game.PlayerState {
	// Mouse look
	s.Yaw -= float32(ev.MouseDX * mouseSens)
	s.Pitch -= float32(ev.MouseDY * mouseSens)
	if s.Pitch > 89 {
		s.Pitch = 89
	}
	if s.Pitch < -89 {
		s.Pitch = -89
	}

	dt := float32(ev.Dt)
	if dt <= 0 {
		return s
	}

	// No clip nodes → noclip fallback
	if len(m.ClipNodes) == 0 || len(m.Models) == 0 {
		return noclip(m, s, ev, dt)
	}

	hull := m.Models[0].HeadNodes[1] // standing player hull

	// Player origin = eye position minus view height
	origin := [3]float32{s.Position[0], s.Position[1], s.Position[2] - eyeHeight}

	// Advance entity state machines before movement
	mgr.Update(dt, m, origin)

	yaw := float64(mgl32.DegToRad(s.Yaw))
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
	s.Velocity[0] = wishX
	s.Velocity[1] = wishY

	// Apply gravity when airborne
	if !s.OnGround {
		s.Velocity[2] -= gravity * dt
	}

	// Jump
	if ev.Keys[glfw.KeySpace] && s.OnGround {
		s.Velocity[2] = jumpSpeed
		s.OnGround = false
	}

	// Slide move
	disp := [3]float32{s.Velocity[0] * dt, s.Velocity[1] * dt, s.Velocity[2] * dt}
	newOrigin := slideMove(m, mgr.Entities, hull, origin, disp)

	// Ground check: trace 2 units down from new position
	groundEnd := [3]float32{newOrigin[0], newOrigin[1], newOrigin[2] - 2}
	gtr := traceAll(m, mgr.Entities, hull, newOrigin, groundEnd)
	if gtr.Hit && gtr.Normal[2] > 0.7 {
		s.OnGround = true
		s.Velocity[2] = 0
		newOrigin = gtr.EndPos
	} else {
		s.OnGround = false
	}

	s.Position = mgl32.Vec3{newOrigin[0], newOrigin[1], newOrigin[2] + eyeHeight}
	s.LeafIndex = vis.LeafForPoint(m, [3]float32(s.Position))
	s.Entities = mgr.States()

	return s
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
func noclip(m *bsp.Map, s game.PlayerState, ev game.InputEvent, dt float32) game.PlayerState {
	yaw := float64(mgl32.DegToRad(s.Yaw))
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
	s.Position = s.Position.Add(move)
	s.LeafIndex = vis.LeafForPoint(m, [3]float32(s.Position))
	return s
}
