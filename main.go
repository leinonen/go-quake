package main

import (
	_ "embed"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/go-gl/gl/v4.3-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/go-gl/mathgl/mgl32"
	"go-quake/bsp"
	"go-quake/entities"
	"go-quake/gfx"
	"go-quake/mdl"
	"go-quake/pak"
	"go-quake/physics"
	"go-quake/renderer"
)

func init() {
	runtime.LockOSThread()
}

//go:embed renderer/shaders/pvs_traverse.glsl
var computeSrc string

//go:embed renderer/shaders/world.vert.glsl
var vertSrc string

//go:embed renderer/shaders/world.frag.glsl
var fragSrc string

//go:embed renderer/shaders/skybox.vert.glsl
var skyVertSrc string

//go:embed renderer/shaders/skybox.frag.glsl
var skyFragSrc string

//go:embed renderer/shaders/weapon.vert.glsl
var weapVertSrc string

//go:embed renderer/shaders/weapon.frag.glsl
var weapFragSrc string

//go:embed renderer/shaders/hud.vert.glsl
var hudVertSrc string

//go:embed renderer/shaders/hud.frag.glsl
var hudFragSrc string

//go:embed renderer/shaders/particle.vert.glsl
var partVertSrc string

//go:embed renderer/shaders/particle.frag.glsl
var partFragSrc string

//go:embed renderer/shaders/underwater.vert.glsl
var uwVertSrc string

//go:embed renderer/shaders/underwater.frag.glsl
var uwFragSrc string

const eyeHeight = 22.0

func main() {
	pakPath := flag.String("pak", "", "path to id1 directory containing pak*.pak files")
	mapName := flag.String("map", "", "map name (e.g. e1m1) or path to .bsp file")
	flag.Parse()

	var m *bsp.Map
	var palette []byte
	var allWeaponModels [8]renderer.WeaponModel
	var weaponFrameCounts [8]int
	var itemModels []renderer.ItemModel
	var initItems []physics.ItemState
	var itemSpawns []entities.ItemSpawn
	var monsterStates []entities.MonsterState
	var flameStates []entities.FlameState
	var hudAssets *renderer.HUDAssets

	switch {
	case *pakPath != "":
		// Load all pak*.pak files from the given directory
		p, err := pak.OpenDir(*pakPath)
		if err != nil {
			log.Fatalf("open pak dir: %v", err)
		}
		defer p.Close()

		// If no map specified, list available maps and exit
		if *mapName == "" {
			maps := p.FindMaps()
			fmt.Println("Available maps in PAK:")
			for _, n := range maps {
				fmt.Println(" ", n)
			}
			if len(maps) == 0 {
				fmt.Println("  (none found)")
			}
			os.Exit(0)
		}

		// Load palette for texture colour conversion
		palette, _ = p.ReadFile("gfx/palette.lmp")

		// Load HUD sprite assets.
		hudAssets = loadHUDAssets(p, palette)

		// Load view weapon MDLs for all 8 weapon slots.
		viewWeaponPaths := [8]string{
			"progs/v_axe.mdl",   // 0 = Axe
			"progs/v_shot.mdl",  // 1 = Shotgun
			"progs/v_shot.mdl",  // 2 = Super Shotgun (same MDL)
			"progs/v_nail.mdl",  // 3 = Nailgun
			"progs/v_nail2.mdl", // 4 = Super Nailgun
			"progs/v_rock.mdl",  // 5 = Grenade Launcher
			"progs/v_rock2.mdl", // 6 = Rocket Launcher
			"progs/v_light.mdl", // 7 = Lightning Gun
		}
		for slot, path := range viewWeaponPaths {
			wdata, werr := p.ReadFile(path)
			if werr != nil {
				log.Printf("view weapon not in PAK: %s", path)
				continue
			}
			wmdl, werr := mdl.Load(wdata)
			if werr != nil {
				log.Printf("view weapon parse %s: %v", path, werr)
				continue
			}
			nf := wmdl.NumFrames()
			if nf <= 0 {
				nf = 1
			}
			texRGB := wmdl.SkinRGB(0, palette)
			var frames []*renderer.WeaponMesh
			for f := 0; f < nf; f++ {
				verts := wmdl.BuildVerts(f)
				if len(verts) > 0 && len(texRGB) > 0 {
					frames = append(frames, &renderer.WeaponMesh{
						Verts:  verts,
						TexRGB: texRGB,
						TexW:   wmdl.SkinWidth,
						TexH:   wmdl.SkinHeight,
					})
				} else {
					frames = append(frames, nil)
				}
			}
			allWeaponModels[slot] = renderer.WeaponModel{Frames: frames}
			weaponFrameCounts[slot] = nf
			log.Printf("view weapon slot %d (%s): %d frames", slot, path, nf)
		}

		// Normalise: accept "e1m1", "e1m1.bsp", "maps/e1m1.bsp"
		name := *mapName
		if !strings.Contains(name, "/") {
			name = "maps/" + name
		}
		if !strings.HasSuffix(name, ".bsp") {
			name += ".bsp"
		}

		data, err := p.ReadFile(name)
		if err != nil {
			log.Fatalf("read map from pak: %v", err)
		}
		m, err = bsp.LoadBytes(data)
		if err != nil {
			log.Fatalf("parse bsp: %v", err)
		}

		// Load item models (MDL or BSP sub-model) referenced by the map entity lump.
		itemSpawns = entities.ParseItems(m.Entities)
		modelPathToIdx := map[string]int{}

		for _, sp := range itemSpawns {
			if _, seen := modelPathToIdx[sp.ModelPath]; !seen {
				modelPathToIdx[sp.ModelPath] = len(itemModels)
				im := loadItemModel(p, sp.ModelPath, palette)
				itemModels = append(itemModels, im)
			}
			initItems = append(initItems, physics.ItemState{
				Pos:    sp.Pos,
				MdlIdx: modelPathToIdx[sp.ModelPath],
			})
		}

		// Warn about item-like classnames we have no mapping for
		for _, e := range bsp.ParseEntities(m.Entities) {
			class := e.Fields["classname"]
			if (strings.HasPrefix(class, "item_") || strings.HasPrefix(class, "weapon_") || strings.HasPrefix(class, "ammo_")) && entities.ItemPath(e) == "" {
				log.Printf("item classname not mapped: %s", class)
			}
		}
		log.Printf("items: %d spawns, %d unique models", len(initItems), len(itemModels))

		// Load monster MDL models — all animation frames.
		monsterSpawns := entities.ParseMonsters(m.Entities)
		monsterFrameNames := map[string][]string{}
		for _, sp := range monsterSpawns {
			if _, seen := modelPathToIdx[sp.ModelPath]; !seen {
				modelPathToIdx[sp.ModelPath] = len(itemModels)
				im, names := loadMDLAllFrames(p, sp.ModelPath, palette)
				monsterFrameNames[sp.ModelPath] = names
				itemModels = append(itemModels, im)
			}
			mdlIdx := modelPathToIdx[sp.ModelPath]
			monsterStates = append(monsterStates, entities.NewMonsterState(sp, mdlIdx, monsterFrameNames[sp.ModelPath]))
		}
		log.Printf("monsters: %d spawns, %d unique models", len(monsterSpawns), len(modelPathToIdx)-len(itemSpawns))

		// Load flame MDL models — all animation frames.
		flameSpawns := entities.ParseFlames(m.Entities)
		for _, sp := range flameSpawns {
			if _, seen := modelPathToIdx[sp.ModelPath]; !seen {
				modelPathToIdx[sp.ModelPath] = len(itemModels)
				im, _ := loadMDLAllFrames(p, sp.ModelPath, palette)
				itemModels = append(itemModels, im)
			}
			mdlIdx := modelPathToIdx[sp.ModelPath]
			nf := len(itemModels[mdlIdx].Frames)
			if nf <= 0 {
				nf = 1
			}
			flameStates = append(flameStates, entities.FlameState{
				Pos:       sp.Pos,
				MdlIdx:    mdlIdx,
				NumFrames: nf,
			})
		}
		log.Printf("flames: %d spawns", len(flameStates))

	case *mapName != "":
		// Direct .bsp file
		var err error
		m, err = bsp.Load(*mapName)
		if err != nil {
			log.Fatalf("load bsp: %v", err)
		}

	default:
		fmt.Fprintln(os.Stderr, "usage:")
		fmt.Fprintln(os.Stderr, "  go-quake -pak /path/to/id1 -map e1m1")
		fmt.Fprintln(os.Stderr, "  go-quake -pak /path/to/id1          (list maps)")
		fmt.Fprintln(os.Stderr, "  go-quake -map /path/to/map.bsp")
		os.Exit(1)
	}

	log.Printf("BSP loaded: %d leaves, %d nodes, %d faces, %d vertices",
		len(m.Leaves), len(m.Nodes), len(m.Faces), len(m.Vertices))

	// Spawn position
	var spawn mgl32.Vec3
	if org, ok := m.SpawnPoint(); ok {
		spawn = mgl32.Vec3{org[0], org[1], org[2] + eyeHeight}
		log.Printf("spawn from info_player_start: %.0f %.0f %.0f", org[0], org[1], org[2])
	} else if len(m.Models) > 0 {
		mo := m.Models[0]
		spawn = mgl32.Vec3{
			(mo.Mins[0] + mo.Maxs[0]) / 2,
			(mo.Mins[1] + mo.Maxs[1]) / 2,
			mo.Mins[2] + eyeHeight,
		}
		log.Printf("spawn fallback to AABB centre: %.0f %.0f %.0f", spawn[0], spawn[1], spawn[2])
	}

	// GLFW + GL
	if err := glfw.Init(); err != nil {
		log.Fatalf("glfw init: %v", err)
	}
	defer glfw.Terminate()

	glfw.WindowHint(glfw.ContextVersionMajor, 4)
	glfw.WindowHint(glfw.ContextVersionMinor, 3)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)

	win, err := glfw.CreateWindow(1280, 720, "go-quake", nil, nil)
	if err != nil {
		log.Fatalf("create window: %v", err)
	}
	win.MakeContextCurrent()
	glfw.SwapInterval(1)
	win.SetInputMode(glfw.CursorMode, glfw.CursorDisabled)

	if err := gl.Init(); err != nil {
		log.Fatalf("gl init: %v", err)
	}

	rend, err := renderer.Init(m,
		vertSrc, fragSrc, computeSrc,
		skyVertSrc, skyFragSrc,
		weapVertSrc, weapFragSrc,
		hudVertSrc, hudFragSrc,
		partVertSrc, partFragSrc,
		uwVertSrc, uwFragSrc,
		palette, allWeaponModels[:], itemModels, hudAssets)
	if err != nil {
		log.Fatalf("renderer init: %v", err)
	}

	mgr := entities.NewManager(m)
	log.Printf("brush entities: %d (func_door/func_plat)", len(mgr.Entities))

	phys := physics.New(win, m, mgr, spawn, itemSpawns, initItems, monsterStates, flameStates, weaponFrameCounts)

	var screenshotRequested bool
	win.SetKeyCallback(func(w *glfw.Window, key glfw.Key, _ int, action glfw.Action, _ glfw.ModifierKey) {
		if action == glfw.Press {
			switch key {
			case glfw.KeyEscape:
				w.SetShouldClose(true)
			case glfw.KeyF12:
				screenshotRequested = true
			}
		}
	})

	var prevTime = time.Now()
	var debugTick uint

	for !win.ShouldClose() {
		now := time.Now()
		dt := float32(now.Sub(prevTime).Seconds())
		prevTime = now
		if dt > 0.1 {
			dt = 0.1 // cap
		}

		glfw.PollEvents()
		phys.Tick(dt)

		w, h := win.GetFramebufferSize()
		gl.Viewport(0, 0, int32(w), int32(h))

		rend.Draw(phys, w, h)

		if screenshotRequested {
			screenshotRequested = false
			saveScreenshot(w, h)
		}

		debugTick++
		if debugTick%30 == 0 {
			pos := [3]float32(phys.Pos)
			title := fmt.Sprintf("go-quake | HP: %d | leaf %d | pos: %.0f %.0f %.0f",
				phys.Health, phys.LeafIndex, pos[0], pos[1], pos[2])
			win.SetTitle(title)
		}

		win.SwapBuffers()
	}

	win.SetInputMode(glfw.CursorMode, glfw.CursorNormal)
	glfw.PollEvents()
}

// pakReader abstracts ReadFile from both pak.PAK and pak.MultiPAK.
type pakReader interface {
	ReadFile(name string) ([]byte, error)
}

// loadHUDAssets reads and decodes LMP sprites for the in-game status bar.
// Missing files are silently skipped; the renderer falls back gracefully.
func loadHUDAssets(p pakReader, palette []byte) *renderer.HUDAssets {
	decodeLMP := func(path string) *gfx.LMPImage {
		data, err := p.ReadFile(path)
		if err != nil {
			return nil
		}
		img, err := gfx.DecodeLMP(data, palette)
		if err != nil {
			log.Printf("HUD: decode %s: %v", path, err)
			return nil
		}
		return img
	}

	a := &renderer.HUDAssets{}
	a.SBar = decodeLMP("gfx/sbar.lmp")
	if a.SBar == nil {
		a.SBar = gfx.GenerateSBar()
	}

	for i := 0; i <= 9; i++ {
		a.Nums[i] = decodeLMP(fmt.Sprintf("gfx/num_%d.lmp", i))
		if a.Nums[i] == nil {
			a.Nums[i] = gfx.GenerateDigit(i)
		}
	}

	// Face sprites: try gfx/face1.lmp … gfx/face5.lmp first, then face1_0.lmp variant.
	for i := 0; i < 5; i++ {
		img := decodeLMP(fmt.Sprintf("gfx/face%d.lmp", i+1))
		if img == nil {
			img = decodeLMP(fmt.Sprintf("gfx/face%d_0.lmp", i+1))
		}
		if img == nil {
			img = gfx.GenerateFace(i)
		}
		a.Faces[i] = img
	}

	log.Printf("HUD assets loaded (sbar=%v, nums=%v, faces=%v)",
		a.SBar != nil,
		func() int {
			n := 0
			for _, d := range a.Nums {
				if d != nil {
					n++
				}
			}
			return n
		}(),
		func() int {
			n := 0
			for _, f := range a.Faces {
				if f != nil {
					n++
				}
			}
			return n
		}(),
	)
	return a
}

// loadItemModel loads one item model (MDL or BSP) and returns an ItemModel with all frames.
// For MDL: one frame (frame 0 only — items don't animate).
// For BSP: one frame with multiple texture groups.
func loadItemModel(p pakReader, modelPath string, palette []byte) renderer.ItemModel {
	idata, err := p.ReadFile(modelPath)
	if err != nil {
		log.Printf("item model not in PAK: %s", modelPath)
		return renderer.ItemModel{}
	}
	if strings.HasSuffix(modelPath, ".mdl") {
		imdl, err := mdl.Load(idata)
		if err != nil {
			log.Printf("item MDL parse failed: %s: %v", modelPath, err)
			return renderer.ItemModel{}
		}
		verts := imdl.BuildVerts(0)
		texRGB := imdl.SkinRGB(0, palette)
		if len(verts) == 0 || len(texRGB) == 0 {
			return renderer.ItemModel{}
		}
		log.Printf("item MDL loaded:  %s (%d tris)", modelPath, len(verts)/15)
		return renderer.ItemModel{
			Frames: [][]*renderer.WeaponMesh{{{
				Verts:  verts,
				TexRGB: texRGB,
				TexW:   imdl.SkinWidth,
				TexH:   imdl.SkinHeight,
			}}},
		}
	}
	if strings.HasSuffix(modelPath, ".bsp") {
		groups, err := renderer.BuildBSPItemMesh(idata, palette)
		if err != nil || len(groups) == 0 {
			log.Printf("item BSP parse failed: %s: %v", modelPath, err)
			return renderer.ItemModel{}
		}
		total := 0
		for _, g := range groups {
			total += len(g.Verts) / 15
		}
		log.Printf("item BSP loaded:  %s (%d tris, %d textures)", modelPath, total, len(groups))
		return renderer.ItemModel{Frames: [][]*renderer.WeaponMesh{groups}}
	}
	return renderer.ItemModel{}
}

// loadMDLAllFrames loads a monster MDL and returns an ItemModel with all animation frames
// plus the per-frame animation names (e.g. "stand1", "run3") for state-driven animation.
func loadMDLAllFrames(p pakReader, modelPath string, palette []byte) (renderer.ItemModel, []string) {
	mdata, err := p.ReadFile(modelPath)
	if err != nil {
		log.Printf("monster MDL not in PAK: %s", modelPath)
		return renderer.ItemModel{}, nil
	}
	mmdl, err := mdl.Load(mdata)
	if err != nil {
		log.Printf("monster MDL parse failed: %s: %v", modelPath, err)
		return renderer.ItemModel{}, nil
	}
	nf := mmdl.NumFrames()
	if nf <= 0 {
		nf = 1
	}
	texRGB := mmdl.SkinRGB(0, palette)
	var frames [][]*renderer.WeaponMesh
	for f := 0; f < nf; f++ {
		verts := mmdl.BuildVerts(f)
		if len(verts) == 0 || len(texRGB) == 0 {
			frames = append(frames, nil)
			continue
		}
		frames = append(frames, []*renderer.WeaponMesh{{
			Verts:  verts,
			TexRGB: texRGB,
			TexW:   mmdl.SkinWidth,
			TexH:   mmdl.SkinHeight,
		}})
	}
	if len(frames) > 0 && frames[0] != nil {
		log.Printf("monster MDL loaded: %s (%d frames, %d tris)", modelPath, nf, len(frames[0][0].Verts)/15)
	}
	return renderer.ItemModel{Frames: frames}, mmdl.FrameNames()
}

func saveScreenshot(w, h int) {
	pixels := make([]byte, w*h*3)
	gl.ReadPixels(0, 0, int32(w), int32(h), gl.RGB, gl.UNSIGNED_BYTE, gl.Ptr(pixels))

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			img.SetNRGBA(x, h-1-y, color.NRGBA{R: pixels[i], G: pixels[i+1], B: pixels[i+2], A: 255})
		}
	}

	filename := fmt.Sprintf("screenshot_%s.png", time.Now().Format("20060102_150405"))
	f, err := os.Create(filename)
	if err != nil {
		log.Printf("screenshot: %v", err)
		return
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Printf("screenshot encode: %v", err)
		return
	}
	log.Printf("screenshot saved: %s", filename)
}
