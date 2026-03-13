package game

// Bus holds all inter-goroutine channels.
type Bus struct {
	Input    chan InputEvent  // input → physics (unbuffered: honest backpressure)
	Physics  chan PlayerState // physics → coordinator (buffered 1: drop stale)
	Render   chan RenderFrame // coordinator → renderer (buffered 1: drop stale)
	Shutdown chan struct{}    // closed to broadcast stop
}

// NewBus creates a Bus with appropriate channel sizes.
func NewBus() *Bus {
	return &Bus{
		Input:    make(chan InputEvent),
		Physics:  make(chan PlayerState, 1),
		Render:   make(chan RenderFrame, 1),
		Shutdown: make(chan struct{}),
	}
}
