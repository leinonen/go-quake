package entities

import (
	"strconv"

	"go-quake/bsp"
)

// ItemSpawn holds the world position and model path (.mdl or .bsp) for one item entity.
type ItemSpawn struct {
	Pos       [3]float32
	ModelPath string
}

// ParseItems returns all item spawns from a BSP entity lump.
// Supports both MDL-based pickups (weapons, armor) and BSP sub-model pickups (health, ammo, keys).
func ParseItems(entityLump string) []ItemSpawn {
	var out []ItemSpawn
	for _, e := range bsp.ParseEntities(entityLump) {
		path := ItemPath(e)
		if path == "" {
			continue
		}
		pos, err := bsp.ParseVec3(e.Fields["origin"])
		if err != nil {
			continue
		}
		out = append(out, ItemSpawn{Pos: pos, ModelPath: path})
	}
	return out
}

// ItemPath returns the PAK model path for a BSP entity, or "" if not a renderable item.
func ItemPath(e bsp.Entity) string {
	flags, _ := strconv.Atoi(e.Fields["spawnflags"])
	switch e.Fields["classname"] {
	// Ground weapons (MDL)
	case "weapon_shotgun", "weapon_supershotgun":
		return "progs/g_shot.mdl"
	case "weapon_nailgun":
		return "progs/g_nail.mdl"
	case "weapon_supernailgun":
		return "progs/g_nail2.mdl"
	case "weapon_grenadelauncher":
		return "progs/g_rock.mdl"
	case "weapon_rocketlauncher":
		return "progs/g_rock2.mdl"
	case "weapon_lightning":
		return "progs/g_light.mdl"
	// Armor (MDL)
	case "item_armor1", "item_armor2", "item_armorInv":
		return "progs/armor.mdl"
	// Powerups (MDL) — classnames match Quake 1 QC exactly; MDL filenames are truncated
	case "item_artifact_envirosuit":
		return "progs/suit.mdl"
	case "item_artifact_super_damage":
		return "progs/quaddama.mdl"
	case "item_artifact_invisibility":
		return "progs/invisibl.mdl"
	case "item_artifact_invulnerability":
		return "progs/invulner.mdl"
	// Health packs (BSP sub-model) — spawnflags&2 = megahealth
	case "item_health":
		if flags&2 != 0 {
			return "maps/b_batt1.bsp"
		}
		return "maps/b_batt0.bsp"
	// Ammo (BSP sub-model) — spawnflags&1 = large box
	// Both ammo_* and item_* names are used in different maps.
	case "ammo_shells", "item_shells":
		if flags&1 != 0 {
			return "maps/b_shell1.bsp"
		}
		return "maps/b_shell0.bsp"
	case "ammo_nails", "item_spikes":
		if flags&1 != 0 {
			return "maps/b_nail1.bsp"
		}
		return "maps/b_nail0.bsp"
	case "ammo_rockets", "item_rockets":
		if flags&1 != 0 {
			return "maps/b_rock1.bsp"
		}
		return "maps/b_rock0.bsp"
	case "ammo_cells":
		if flags&1 != 0 {
			return "maps/b_batt1.bsp"
		}
		return "maps/b_batt0.bsp"
	// Keys and sigils (BSP sub-model)
	case "item_key1":
		return "maps/b_key1.bsp"
	case "item_key2":
		return "maps/b_key2.bsp"
	case "item_sigil":
		return "maps/b_explob.bsp"
	}
	return ""
}
