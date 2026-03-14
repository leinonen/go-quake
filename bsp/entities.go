package bsp

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Entity is a parsed BSP entity (key→value map).
type Entity struct{ Fields map[string]string }

// ParseEntities parses the raw entity lump text into a slice of Entity.
func ParseEntities(lump string) []Entity {
	var entities []Entity
	text := lump
	for {
		start := strings.Index(text, "{")
		end := strings.Index(text, "}")
		if start < 0 || end < 0 || end < start {
			break
		}
		block := text[start+1 : end]
		text = text[end+1:]
		fields := parseEntityFields(block)
		if len(fields) > 0 {
			entities = append(entities, Entity{Fields: fields})
		}
	}
	return entities
}

func parseEntityFields(block string) map[string]string {
	fields := make(map[string]string)
	rest := block
	for {
		qi := strings.Index(rest, `"`)
		if qi < 0 {
			break
		}
		rest = rest[qi+1:]
		ke := strings.Index(rest, `"`)
		if ke < 0 {
			break
		}
		key := rest[:ke]
		rest = rest[ke+1:]

		vi := strings.Index(rest, `"`)
		if vi < 0 {
			break
		}
		rest = rest[vi+1:]
		ve := strings.Index(rest, `"`)
		if ve < 0 {
			break
		}
		val := rest[:ve]
		rest = rest[ve+1:]
		fields[key] = val
	}
	return fields
}

// ParseVec3 parses a "x y z" string into [3]float32.
func ParseVec3(s string) ([3]float32, error) {
	var x, y, z float32
	if n, err := fmt.Sscanf(s, "%f %f %f", &x, &y, &z); n != 3 {
		return [3]float32{}, fmt.Errorf("ParseVec3: got %d components from %q: %v", n, s, err)
	}
	return [3]float32{x, y, z}, nil
}

// ParseFloat parses a float string, returning def on failure.
func ParseFloat(s string, def float32) float32 {
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 32)
	if err != nil {
		return def
	}
	return float32(v)
}

// MoveDir converts a Quake angle value to a movement direction vector.
// angle -1 → up (0,0,1), -2 → down (0,0,-1), else → (cos°, sin°, 0).
func MoveDir(angle float32) [3]float32 {
	switch angle {
	case -1:
		return [3]float32{0, 0, 1}
	case -2:
		return [3]float32{0, 0, -1}
	default:
		rad := float64(angle) * math.Pi / 180.0
		return [3]float32{float32(math.Cos(rad)), float32(math.Sin(rad)), 0}
	}
}
