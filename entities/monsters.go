package entities

import "strings"

// AnimRange is an inclusive [Start, End] frame index range within an MDL.
type AnimRange struct{ Start, End int }

// Valid returns true if the range refers to at least one frame.
func (r AnimRange) Valid() bool { return r.Start >= 0 && r.End >= r.Start }

// MonsterAnimState is the current animation mode of a monster.
type MonsterAnimState int

const (
	AnimIdle MonsterAnimState = iota
	AnimRun
	AnimPain
	AnimDead
)

// MonsterState holds the runtime state for one monster instance.
type MonsterState struct {
	Spawn          ItemSpawn
	Pos            [3]float32
	VelZ           float32 // vertical velocity for gravity
	HP             int
	PrevHP         int
	Dead           bool
	OnGround       bool
	Alerted        bool
	FrameTime      float32
	FrameIdx       int
	NumFrames      int
	MdlIdx         int // index into renderer itemVAOs
	AttackCooldown float32
	Yaw            float32 // facing angle in radians, updated while chasing
	AnimState      MonsterAnimState
	IdleRange      AnimRange
	RunRange       AnimRange
	PainRange      AnimRange
	DeadRange      AnimRange
}

// FlameState holds runtime animation state for one flame entity.
// Flames are purely decorative: no AI, no gravity, no collision.
type FlameState struct {
	Pos       [3]float32
	MdlIdx    int
	FrameIdx  int
	FrameTime float32
	NumFrames int
}

const (
	MonsterHP             = 30
	MonsterSpeed          = 150.0
	MonsterMeleeDist      = 64.0
	MonsterDamage         = 10
	MonsterFPS            = 10.0
	MonsterAttackCooldown = 1.5
)

// NewMonsterState creates a runtime monster state from a spawn point.
// frameNames are the per-frame animation names from the MDL (e.g. "stand1", "run3").
func NewMonsterState(sp ItemSpawn, mdlIdx int, frameNames []string) MonsterState {
	nf := len(frameNames)
	if nf <= 0 {
		nf = 1
	}
	all := AnimRange{0, nf - 1}
	idle := findAnimRange(frameNames, "stand", "idle")
	if !idle.Valid() {
		idle = all
	}
	run := findAnimRange(frameNames, "run", "walk")
	if !run.Valid() {
		run = idle
	}
	pain := findAnimRange(frameNames, "pain")
	dead := findAnimRange(frameNames, "death", "die", "dth")

	return MonsterState{
		Spawn:     sp,
		Pos:       sp.Pos,
		HP:        MonsterHP,
		PrevHP:    MonsterHP,
		NumFrames: nf,
		MdlIdx:    mdlIdx,
		FrameIdx:  idle.Start,
		IdleRange: idle,
		RunRange:  run,
		PainRange: pain,
		DeadRange: dead,
	}
}

// findAnimRange scans frameNames for the first contiguous group whose names match
// any of the given prefixes immediately followed by a digit, returning its [start, end] range.
func findAnimRange(names []string, prefixes ...string) AnimRange {
	for _, prefix := range prefixes {
		start, end := -1, -1
		for i, name := range names {
			if animPrefixMatch(name, prefix) {
				if start < 0 {
					start = i
				}
				end = i
			} else if start >= 0 {
				break // stop at the first gap — only the first contiguous block
			}
		}
		if start >= 0 {
			return AnimRange{Start: start, End: end}
		}
	}
	return AnimRange{Start: -1, End: -1}
}

// animPrefixMatch returns true when name starts with prefix followed immediately by a digit.
// This prevents "die" from matching "dieb1" or "death" from matching "deathc1".
func animPrefixMatch(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	return len(rest) > 0 && rest[0] >= '0' && rest[0] <= '9'
}
