package physics

import (
	"math"

	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/game"
	"go-quake/vis"
)

const (
	mouseSens   = 0.15
	moveSpeed   = 320.0 // units/s, classic Quake speed
	eyeHeight   = 22.0  // units above floor
)

// Run is the physics goroutine. It receives input events and emits player states.
func Run(m *bsp.Map, bus *game.Bus, spawn mgl32.Vec3) {
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
			state = tick(m, state, ev)
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

func tick(m *bsp.Map, s game.PlayerState, ev game.InputEvent) game.PlayerState {
	// Mouse look
	s.Yaw -= float32(ev.MouseDX * mouseSens)
	s.Pitch -= float32(ev.MouseDY * mouseSens)
	if s.Pitch > 89 {
		s.Pitch = 89
	}
	if s.Pitch < -89 {
		s.Pitch = -89
	}

	yaw := float64(mgl32.DegToRad(s.Yaw))
	forward := [3]float32{
		float32(math.Cos(yaw)),
		float32(math.Sin(yaw)),
		0,
	}
	right := [3]float32{
		float32(math.Sin(yaw)),
		float32(-math.Cos(yaw)),
		0,
	}

	speed := float32(moveSpeed * ev.Dt)
	move := mgl32.Vec3{}

	if ev.Keys[glfw.KeyW] || ev.Keys[glfw.KeyUp] {
		move = move.Add(mgl32.Vec3(forward).Mul(speed))
	}
	if ev.Keys[glfw.KeyS] || ev.Keys[glfw.KeyDown] {
		move = move.Sub(mgl32.Vec3(forward).Mul(speed))
	}
	if ev.Keys[glfw.KeyA] {
		move = move.Sub(mgl32.Vec3(right).Mul(speed))
	}
	if ev.Keys[glfw.KeyD] {
		move = move.Add(mgl32.Vec3(right).Mul(speed))
	}
	if ev.Keys[glfw.KeySpace] {
		move[2] += speed
	}
	if ev.Keys[glfw.KeyLeftControl] || ev.Keys[glfw.KeyC] {
		move[2] -= speed
	}

	s.Position = s.Position.Add(move)
	s.LeafIndex = vis.LeafForPoint(m, [3]float32(s.Position))

	return s
}
