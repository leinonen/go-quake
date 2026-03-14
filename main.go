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
	"go-quake/game"
	"go-quake/input"
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

const eyeHeight = 22.0

func main() {
	pakPath := flag.String("pak", "", "path to PAK file (e.g. pak0.pak)")
	mapName := flag.String("map", "", "map name inside PAK (e.g. e1m1) or path to .bsp file")
	flag.Parse()

	var m *bsp.Map
	var palette []byte
	var weapon *renderer.WeaponMesh

	switch {
	case *pakPath != "":
		// Load from PAK
		p, err := pak.Open(*pakPath)
		if err != nil {
			log.Fatalf("open pak: %v", err)
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

		// Load view axe model
		if axeData, err := p.ReadFile("progs/v_axe.mdl"); err == nil {
			if axeMDL, err := mdl.Load(axeData); err == nil {
				verts := axeMDL.BuildVerts(0)
				texRGB := axeMDL.SkinRGB(0, palette)
				if len(verts) > 0 && len(texRGB) > 0 {
					weapon = &renderer.WeaponMesh{
						Verts:  verts,
						TexRGB: texRGB,
						TexW:   axeMDL.SkinWidth,
						TexH:   axeMDL.SkinHeight,
					}
					log.Printf("v_axe.mdl loaded: %d tris, skin %dx%d",
						len(verts)/15, axeMDL.SkinWidth, axeMDL.SkinHeight)
				}
			} else {
				log.Printf("v_axe.mdl parse: %v", err)
			}
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

	case *mapName != "":
		// Direct .bsp file
		var err error
		m, err = bsp.Load(*mapName)
		if err != nil {
			log.Fatalf("load bsp: %v", err)
		}

	default:
		fmt.Fprintln(os.Stderr, "usage:")
		fmt.Fprintln(os.Stderr, "  go-quake -pak pak0.pak -map e1m1")
		fmt.Fprintln(os.Stderr, "  go-quake -pak pak0.pak          (list maps)")
		fmt.Fprintln(os.Stderr, "  go-quake -map /path/to/map.bsp")
		os.Exit(1)
	}

	log.Printf("BSP loaded: %d leaves, %d nodes, %d faces, %d vertices",
		len(m.Leaves), len(m.Nodes), len(m.Faces), len(m.Vertices))

	// Spawn position: parse info_player_start from entities, fall back to AABB centre.
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

	rend, err := renderer.Init(m, vertSrc, fragSrc, computeSrc, skyVertSrc, skyFragSrc, weapVertSrc, weapFragSrc, palette, weapon)
	if err != nil {
		log.Fatalf("renderer init: %v", err)
	}

	mgr := entities.NewManager(m)
	log.Printf("brush entities: %d (func_door/func_plat)", len(mgr.Entities))

	bus := game.NewBus()
	go physics.Run(m, mgr, bus, spawn)

	playerState := game.PlayerState{Position: spawn}

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

	// Watchdog: if the main loop stalls for >3s, dump all goroutine stacks.
	watchdog := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-watchdog:
				// heartbeat received, keep watching
			case <-time.After(3 * time.Second):
				buf := make([]byte, 1<<20)
				n := runtime.Stack(buf, true)
				fmt.Fprintf(os.Stderr, "\n=== HANG DETECTED — goroutine dump ===\n%s\n", buf[:n])
			}
		}
	}()

	var lastTime = time.Now()
	var debugTick uint

	for !win.ShouldClose() {
		select {
		case watchdog <- struct{}{}:
		default:
		}
		glfw.PollEvents()
		input.Pump(win, bus, &lastTime)

		select {
		case ps := <-bus.Physics:
			playerState = ps
		default:
		}

		w, h := win.GetFramebufferSize()
		gl.Viewport(0, 0, int32(w), int32(h))

		rend.Draw(game.RenderFrame{Player: playerState}, w, h)

		if screenshotRequested {
			screenshotRequested = false
			saveScreenshot(w, h)
		}

		debugTick++
		if debugTick%30 == 0 {
			pos := [3]float32(playerState.Position)
			title := fmt.Sprintf("go-quake | leaf %d | pos: %.0f %.0f %.0f",
				playerState.LeafIndex, pos[0], pos[1], pos[2])
			win.SetTitle(title)
		}

		win.SwapBuffers()
	}

	close(bus.Shutdown)

	// Restore cursor before GLFW teardown. On Linux/X11, destroying a window
	// with CursorDisabled active can block glfwTerminate waiting for the pointer
	// ungrab to complete. Resetting to Normal and flushing events lets GLFW
	// finish cleanly.
	win.SetInputMode(glfw.CursorMode, glfw.CursorNormal)
	glfw.PollEvents()
}

func saveScreenshot(w, h int) {
	pixels := make([]byte, w*h*3)
	gl.ReadPixels(0, 0, int32(w), int32(h), gl.RGB, gl.UNSIGNED_BYTE, gl.Ptr(pixels))

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 3
			// Flip Y: OpenGL origin is bottom-left, image origin is top-left.
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
