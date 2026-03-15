package input

import (
	"time"

	"github.com/go-gl/glfw/v3.3/glfw"
	"go-quake/game"
)

// Pump captures GLFW input state and sends InputEvents to bus.Input.
// Must be called from the main (GL) thread; we send to a channel for physics.
// Actually: GLFW callbacks run on the main thread, so we snapshot state here.
func Pump(window *glfw.Window, bus *game.Bus, lastTime *time.Time) {
	now := time.Now()
	dt := now.Sub(*lastTime).Seconds()
	*lastTime = now
	if dt > 0.1 {
		dt = 0.1 // cap
	}

	var keys [512]bool
	for _, k := range []glfw.Key{
		glfw.KeyW, glfw.KeyA, glfw.KeyS, glfw.KeyD,
		glfw.KeyUp, glfw.KeyDown,
		glfw.KeySpace, glfw.KeyLeftControl, glfw.KeyC,
		glfw.KeyEscape,
		glfw.Key1, glfw.Key2, glfw.Key3, glfw.Key4,
		glfw.Key5, glfw.Key6, glfw.Key7, glfw.Key8,
	} {
		if int(k) < 512 {
			keys[k] = window.GetKey(k) == glfw.Press
		}
	}

	mx, my := window.GetCursorPos()
	// We reset cursor to centre each frame to accumulate delta
	cx, cy := window.GetSize()
	cxf, cyf := float64(cx)/2, float64(cy)/2
	window.SetCursorPos(cxf, cyf)
	dx := mx - cxf
	dy := my - cyf

	var mouseButtons [8]bool
	for i := 0; i < 8; i++ {
		mouseButtons[i] = window.GetMouseButton(glfw.MouseButton(i)) == glfw.Press
	}

	ev := game.InputEvent{
		Keys:         keys,
		MouseButtons: mouseButtons,
		MouseDX:      dx,
		MouseDY:      dy,
		Dt:           dt,
	}

	// Non-blocking send: drop if physics is busy
	select {
	case bus.Input <- ev:
	default:
	}
}
