package game

import "github.com/go-gl/mathgl/mgl32"

// InputEvent is a snapshot of input state sent from input goroutine to physics.
type InputEvent struct {
	Keys               [512]bool
	MouseButtons       [8]bool
	MouseDX, MouseDY   float64
	Dt                 float64
}

// EntityState carries per-frame render/collision state for one brush entity.
type EntityState struct {
	ModelIndex int
	Offset     [3]float32
}

// ParticleState carries the world position and lifetime for one blood particle.
type ParticleState struct {
	Pos   [3]float32
	Life  float32 // normalised 0..1 remaining lifetime (for alpha fade)
	Stuck bool
}

// PlayerState is the authoritative player position/orientation sent from physics to coordinator.
type PlayerState struct {
	Position      mgl32.Vec3
	Velocity      mgl32.Vec3
	Yaw, Pitch    float32
	LeafIndex     int
	OnGround      bool
	Entities      []EntityState
	Health        int
	WeaponFrame   int
	CurrentWeapon int      // active weapon slot (0=axe, 1=shotgun, ...)
	WeaponAmmo    [8]int   // ammo per type; index matches AmmoShells..AmmoCells constants
	MonsterItems  []ItemState     // live monster positions + frame indices (set by physics)
	Particles     []ParticleState // live blood particles (set by physics)
	InWater       bool            // true when eye position is inside a water leaf
}

// ItemState carries the world position, mesh index, animation frame, and facing yaw for one item or monster.
type ItemState struct {
	Pos    [3]float32
	MdlIdx int
	Frame  int
	Yaw    float32 // facing angle in radians (world Z rotation); 0 = +X direction
}

// RenderFrame is sent from coordinator to renderer each frame.
type RenderFrame struct {
	Player    PlayerState
	Items     []ItemState
	FrameTime float64
}
