package entities

import (
	"fmt"
	"math"

	"go-quake/bsp"
)

// EntityState carries per-frame render/collision state for one brush entity.
type EntityState struct {
	ModelIndex int
	Offset     [3]float32
}

// State is the door/platform animation state.
type State int

const (
	StateClosed  State = iota
	StateOpening
	StateOpen
	StateClosing
)

// BrushEntity represents a moving brush entity (func_door or func_plat).
type BrushEntity struct {
	ModelIndex   int
	ClosedOffset [3]float32
	OpenOffset   [3]float32
	Offset       [3]float32
	Speed        float32
	WaitTime     float32
	State        State
	LerpFrac     float32
	Timer        float32
	Class        string
}

// Manager holds all brush entities.
type Manager struct {
	Entities []*BrushEntity
}

// NewManager parses entity lump and creates a Manager.
func NewManager(m *bsp.Map) *Manager {
	mgr := &Manager{}
	entities := bsp.ParseEntities(m.Entities)
	for _, e := range entities {
		class := e.Fields["classname"]
		if class != "func_door" && class != "func_plat" {
			continue
		}
		modelStr, ok := e.Fields["model"]
		if !ok {
			continue
		}
		var modelIdx int
		if _, err := fmt.Sscanf(modelStr, "*%d", &modelIdx); err != nil {
			continue
		}
		if modelIdx <= 0 || modelIdx >= len(m.Models) {
			continue
		}

		model := m.Models[modelIdx]
		speed := bsp.ParseFloat(e.Fields["speed"], 100)
		wait := bsp.ParseFloat(e.Fields["wait"], 3)

		be := &BrushEntity{
			ModelIndex: modelIdx,
			Speed:      speed,
			WaitTime:   wait,
			Class:      class,
		}

		if class == "func_door" {
			angle := bsp.ParseFloat(e.Fields["angle"], 0)
			moveDir := bsp.MoveDir(angle)
			lip := bsp.ParseFloat(e.Fields["lip"], 8)

			size := [3]float32{
				model.Maxs[0] - model.Mins[0],
				model.Maxs[1] - model.Mins[1],
				model.Maxs[2] - model.Mins[2],
			}
			travelDist := abs32(moveDir[0]*size[0]+moveDir[1]*size[1]+moveDir[2]*size[2]) - lip

			be.ClosedOffset = [3]float32{0, 0, 0}
			be.OpenOffset = [3]float32{
				moveDir[0] * travelDist,
				moveDir[1] * travelDist,
				moveDir[2] * travelDist,
			}
			be.Offset = be.ClosedOffset
			be.State = StateClosed
		} else { // func_plat
			travelDist := model.Maxs[2] - model.Mins[2]
			if h, ok := e.Fields["height"]; ok {
				travelDist = bsp.ParseFloat(h, travelDist)
			}
			be.ClosedOffset = [3]float32{0, 0, -travelDist}
			be.OpenOffset = [3]float32{0, 0, 0}
			be.Offset = be.ClosedOffset
			be.State = StateClosed
		}

		mgr.Entities = append(mgr.Entities, be)
	}
	return mgr
}

// Update advances all entity state machines based on elapsed time and player position.
func (mgr *Manager) Update(dt float32, m *bsp.Map, playerPos [3]float32) {
	for _, e := range mgr.Entities {
		model := m.Models[e.ModelIndex]
		mo := model.Origin

		minX := model.Mins[0] + mo[0] + e.Offset[0] - 64
		minY := model.Mins[1] + mo[1] + e.Offset[1] - 64
		minZ := model.Mins[2] + mo[2] + e.Offset[2] - 64
		maxX := model.Maxs[0] + mo[0] + e.Offset[0] + 64
		maxY := model.Maxs[1] + mo[1] + e.Offset[1] + 64
		maxZ := model.Maxs[2] + mo[2] + e.Offset[2] + 64

		inRange := playerPos[0] >= minX && playerPos[0] <= maxX &&
			playerPos[1] >= minY && playerPos[1] <= maxY &&
			playerPos[2] >= minZ && playerPos[2] <= maxZ

		switch e.State {
		case StateClosed:
			if inRange {
				e.State = StateOpening
			}
		case StateOpening:
			travelDist := dist3(e.ClosedOffset, e.OpenOffset)
			if travelDist <= 0 {
				e.LerpFrac = 1
				e.State = StateOpen
				e.Timer = 0
				break
			}
			e.LerpFrac += (e.Speed / travelDist) * dt
			if e.LerpFrac >= 1 {
				e.LerpFrac = 1
				e.State = StateOpen
				e.Timer = 0
			}
			e.Offset = lerp3(e.ClosedOffset, e.OpenOffset, e.LerpFrac)
		case StateOpen:
			if e.WaitTime >= 0 {
				e.Timer += dt
				if e.Timer >= e.WaitTime {
					e.State = StateClosing
				}
			}
		case StateClosing:
			travelDist := dist3(e.ClosedOffset, e.OpenOffset)
			if travelDist <= 0 {
				e.LerpFrac = 0
				e.State = StateClosed
				break
			}
			e.LerpFrac -= (e.Speed / travelDist) * dt
			if e.LerpFrac <= 0 {
				e.LerpFrac = 0
				e.State = StateClosed
			}
			e.Offset = lerp3(e.ClosedOffset, e.OpenOffset, e.LerpFrac)
			if inRange {
				e.State = StateOpening
			}
		}
	}
}

// States returns the current render state for all entities.
func (mgr *Manager) States() []EntityState {
	states := make([]EntityState, len(mgr.Entities))
	for i, e := range mgr.Entities {
		states[i] = EntityState{
			ModelIndex: e.ModelIndex,
			Offset:     e.Offset,
		}
	}
	return states
}

func lerp3(a, b [3]float32, t float32) [3]float32 {
	return [3]float32{
		a[0] + (b[0]-a[0])*t,
		a[1] + (b[1]-a[1])*t,
		a[2] + (b[2]-a[2])*t,
	}
}

func dist3(a, b [3]float32) float32 {
	dx := a[0] - b[0]
	dy := a[1] - b[1]
	dz := a[2] - b[2]
	return float32(math.Sqrt(float64(dx*dx + dy*dy + dz*dz)))
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
