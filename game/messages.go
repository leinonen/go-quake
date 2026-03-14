package game

import "github.com/go-gl/mathgl/mgl32"

// InputEvent is a snapshot of input state sent from input goroutine to physics.
type InputEvent struct {
	Keys               [512]bool
	MouseDX, MouseDY   float64
	Dt                 float64
}

// EntityState carries per-frame render/collision state for one brush entity.
type EntityState struct {
	ModelIndex int
	Offset     [3]float32
}

// PlayerState is the authoritative player position/orientation sent from physics to coordinator.
type PlayerState struct {
	Position   mgl32.Vec3
	Velocity   mgl32.Vec3
	Yaw, Pitch float32
	LeafIndex  int
	OnGround   bool
	Entities   []EntityState
}

// ItemState carries the world position and mesh index for one item pickup.
type ItemState struct {
	Pos    [3]float32
	MdlIdx int
}

// RenderFrame is sent from coordinator to renderer each frame.
type RenderFrame struct {
	Player    PlayerState
	Items     []ItemState
	FrameTime float64
}
