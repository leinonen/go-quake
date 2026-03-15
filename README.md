# go-quake

Minimalistic Quake 1 engine written in Go. Loads real BSP29 maps and renders them with a compute shader PVS pipeline.

![screenshot](./docs/screenshot_20260314_100833.png)

## Requirements

- Go 1.21+
- OpenGL 4.3
- A Quake 1 installation (for PAK file) or a loose `.bsp` file

## Running

```bash
# Load map from a Quake 1 PAK file
go run . -pak /path/to/id1/pak0.pak -map e1m1

# List all maps in a PAK
go run . -pak /path/to/id1/pak0.pak

# Load a standalone .bsp file (no textures or weapon)
go run . -map /path/to/e1m1.bsp
```

## Controls

| Key / Button | Action |
|---|---|
| WASD | Move |
| Mouse | Look |
| Space | Jump |
| Left mouse button | Axe swing |
| Escape | Quit |
| F12 | Screenshot |

## Features

- **Compute shader PVS** — Quake's portal visibility executed on the GPU; invisible faces are discarded before rasterization
- **Goroutine architecture** — input, physics, and rendering run as separate goroutines communicating over typed channels; vsync is the only throttle
- **BSP collision** — hull tracing against the world and brush entities (func_door, func_plat)
- **Interactive doors and elevators** — proximity-triggered state machines with full collision
- **Procedural skybox** — FBM cloud layers replace Quake sky polygons; no seams from any angle
- **Procedural water** — sin-warp turbulence + caustic glints replace Quake water textures
- **View weapon** — `v_axe.mdl` rendered in camera space with full swing animation
- **Item pickup** — weapons, armor, ammo, health, and keys disappear on contact; health packs restore HP
- **Monster AI** — all 15 Quake monster types animate, alert on line-of-sight, chase, and melee attack; blocked by closed doors; subject to gravity
- **Combat** — left-click swings the axe; hit detection fires at frame 2 of the swing; monsters have 30 HP and die permanently
- **Player health** — starts at 100 HP; monsters deal 10 damage per hit; health bar at screen bottom; death teleports back to spawn
- **Respawn** — on death the player resets to spawn, HP restores to 100, and all monsters un-alert

## License

This project is for educational purposes. Quake 1 game data (PAK files) is not included and remains property of id Software.
