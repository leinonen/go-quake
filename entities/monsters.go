package entities

// MonsterState holds the runtime state for one monster instance.
type MonsterState struct {
	Spawn          ItemSpawn
	Pos            [3]float32
	VelZ           float32 // vertical velocity for gravity
	HP             int
	Dead           bool
	Alerted        bool
	FrameTime      float32
	FrameIdx       int
	NumFrames      int
	MdlIdx         int     // index into renderer itemVAOs
	AttackCooldown float32
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
func NewMonsterState(sp ItemSpawn, mdlIdx, numFrames int) MonsterState {
	nf := numFrames
	if nf <= 0 {
		nf = 1
	}
	return MonsterState{
		Spawn:     sp,
		Pos:       sp.Pos,
		HP:        MonsterHP,
		NumFrames: nf,
		MdlIdx:    mdlIdx,
	}
}
