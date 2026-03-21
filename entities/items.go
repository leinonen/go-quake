package entities

import (
	"strconv"

	"go-quake/bsp"
)

// Weapon type constants — index into the view weapon slot array.
const (
	WeaponNone            = 0
	WeaponShotgun         = 1
	WeaponSuperShotgun    = 2
	WeaponNailgun         = 3
	WeaponSuperNailgun    = 4
	WeaponGrenadeLauncher = 5
	WeaponRocketLauncher  = 6
	WeaponLightning       = 7
)

// Ammo type constants — index into the ammo array.
const (
	AmmoNone    = 0
	AmmoShells  = 1
	AmmoNails   = 2
	AmmoRockets = 3
	AmmoCells   = 4
)

// ItemSpawn holds the world position and model path (.mdl or .bsp) for one item entity.
type ItemSpawn struct {
	Pos              [3]float32
	ModelPath        string
	HealthValue      int     // >0 for health packs; amount of HP restored on pickup
	WeaponType       int     // WeaponNone or WeaponShotgun..WeaponLightning
	AmmoType         int     // AmmoNone or AmmoShells..AmmoCells
	AmmoAmount       int     // amount granted on pickup
	ArmorValue       int     // >0 for armor items; total armor points granted
	ArmorAbsorption  float32 // fraction of incoming damage absorbed by armor (0.3/0.6/0.8)
	Rotates          bool    // true for weapons and armor (spin in place); false for health/ammo
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
		flags, _ := strconv.Atoi(e.Fields["spawnflags"])

		health := 0
		if e.Fields["classname"] == "item_health" {
			if flags&2 != 0 {
				health = 100 // megahealth
			} else {
				health = 25 // normal health
			}
		}

		weaponType := WeaponNone
		switch e.Fields["classname"] {
		case "weapon_shotgun":
			weaponType = WeaponShotgun
		case "weapon_supershotgun":
			weaponType = WeaponSuperShotgun
		case "weapon_nailgun":
			weaponType = WeaponNailgun
		case "weapon_supernailgun":
			weaponType = WeaponSuperNailgun
		case "weapon_grenadelauncher":
			weaponType = WeaponGrenadeLauncher
		case "weapon_rocketlauncher":
			weaponType = WeaponRocketLauncher
		case "weapon_lightning":
			weaponType = WeaponLightning
		}

		ammoType := AmmoNone
		ammoAmount := 0
		switch e.Fields["classname"] {
		case "ammo_shells", "item_shells":
			ammoType = AmmoShells
			if flags&1 != 0 {
				ammoAmount = 40
			} else {
				ammoAmount = 25
			}
		case "ammo_nails", "item_spikes":
			ammoType = AmmoNails
			if flags&1 != 0 {
				ammoAmount = 50
			} else {
				ammoAmount = 25
			}
		case "ammo_rockets", "item_rockets":
			ammoType = AmmoRockets
			if flags&1 != 0 {
				ammoAmount = 10
			} else {
				ammoAmount = 5
			}
		case "ammo_cells":
			ammoType = AmmoCells
			if flags&1 != 0 {
				ammoAmount = 12
			} else {
				ammoAmount = 6
			}
		}

		armorValue := 0
		var armorAbsorption float32
		switch e.Fields["classname"] {
		case "item_armor1":
			armorValue = 100
			armorAbsorption = 0.3
		case "item_armor2":
			armorValue = 150
			armorAbsorption = 0.6
		case "item_armorInv":
			armorValue = 200
			armorAbsorption = 0.8
		}

		rotates := weaponType != WeaponNone || armorValue > 0

		out = append(out, ItemSpawn{
			Pos:             pos,
			ModelPath:       path,
			HealthValue:     health,
			WeaponType:      weaponType,
			AmmoType:        ammoType,
			AmmoAmount:      ammoAmount,
			ArmorValue:      armorValue,
			ArmorAbsorption: armorAbsorption,
			Rotates:         rotates,
		})
	}
	return out
}

// MonsterPath returns the PAK MDL path for a monster classname, or "" if unknown.
func MonsterPath(classname string) string {
	switch classname {
	case "monster_army":
		return "progs/soldier.mdl"
	case "monster_enforcer":
		return "progs/enforcer.mdl"
	case "monster_ogre":
		return "progs/ogre.mdl"
	case "monster_demon1":
		return "progs/demon.mdl"
	case "monster_shambler":
		return "progs/shambler.mdl"
	case "monster_knight":
		return "progs/knight.mdl"
	case "monster_zombie":
		return "progs/zombie.mdl"
	case "monster_dog":
		return "progs/dog.mdl"
	case "monster_hell_knight":
		return "progs/hknight.mdl"
	case "monster_scrag":
		return "progs/wizard.mdl"
	case "monster_tarbaby":
		return "progs/tarbaby.mdl"
	case "monster_fish":
		return "progs/fish.mdl"
	case "monster_shalrath":
		return "progs/shalrath.mdl"
	case "monster_boss":
		return "progs/boss.mdl"
	case "monster_oldone":
		return "progs/oldone.mdl"
	}
	return ""
}

// ParseMonsters returns all monster spawns from a BSP entity lump.
func ParseMonsters(entityLump string) []ItemSpawn {
	var out []ItemSpawn
	for _, e := range bsp.ParseEntities(entityLump) {
		path := MonsterPath(e.Fields["classname"])
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

// FlamePath returns the MDL path for a flame light entity classname, or "".
func FlamePath(classname string) string {
	switch classname {
	case "light_flame_large_yellow", "light_flame_small_yellow",
		"light_flame_large_white", "light_flame_small_white":
		return "progs/flame2.mdl"
	}
	return ""
}

// ParseFlames returns all flame spawns from a BSP entity lump.
func ParseFlames(entityLump string) []ItemSpawn {
	var out []ItemSpawn
	for _, e := range bsp.ParseEntities(entityLump) {
		path := FlamePath(e.Fields["classname"])
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
