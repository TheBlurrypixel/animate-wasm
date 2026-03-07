//go:build js && wasm

package main

import (
	"fmt"
	"math"
	"syscall/js"
	"time"
)

type Layer struct {
	Name   string
	Frames []bool // keyframe markers (simple)
	Color  string
}

type App struct {
	activeMenu string

	docW, docH int
	fps        int

	stageCanvas js.Value
	stageCtx    js.Value

	tlCanvas js.Value
	tlCtx    js.Value

	statusEl   js.Value
	docSizeEl  js.Value
	docFpsEl   js.Value
	curFrameEl js.Value
	isPlayEl   js.Value
	selToolEl  js.Value

	layers []Layer

	// timeline state
	totalFrames int
	curFrame    int // 1-based
	playing     bool

	zoom       float64 // pixels per frame
	layerH     float64
	headerW    float64
	playheadX  float64
	draggingPH bool

	lastTick time.Time

	// stage demo
	foxX float64
}

func main() {
	app := &App{
		docW:        640,
		docH:        360,
		fps:         24,
		totalFrames: 120,
		curFrame:    1,
		zoom:        10,  // px per frame
		layerH:      28,  // px
		headerW:     180, // px
		foxX:        120, // demo actor x
	}

	app.initDOM()
	app.initLayers()
	app.bindUI()
	app.resizeCanvases()
	app.renderAll()

	// animation loop
	app.lastTick = time.Now()
	var raf js.Func
	raf = js.FuncOf(func(this js.Value, args []js.Value) any {
		app.tick()
		app.renderAll()
		js.Global().Call("requestAnimationFrame", raf)
		return nil
	})
	js.Global().Call("requestAnimationFrame", raf)

	select {}
}

func (a *App) initDOM() {
	d := js.Global().Get("document")

	a.stageCanvas = d.Call("getElementById", "stage")
	a.stageCtx = a.stageCanvas.Call("getContext", "2d")

	a.tlCanvas = d.Call("getElementById", "timeline")
	a.tlCtx = a.tlCanvas.Call("getContext", "2d")

	a.statusEl = d.Call("getElementById", "status")
	a.docSizeEl = d.Call("getElementById", "docSize")
	a.docFpsEl = d.Call("getElementById", "docFps")
	a.curFrameEl = d.Call("getElementById", "curFrame")
	a.isPlayEl = d.Call("getElementById", "isPlaying")
	a.selToolEl = d.Call("getElementById", "selTool")

	a.statusEl.Set("textContent", "WASM ready")
	a.docSizeEl.Set("textContent", fmt.Sprintf("%d × %d px", a.docW, a.docH))
	a.docFpsEl.Set("textContent", fmt.Sprintf("%d", a.fps))
}

func (a *App) initLayers() {
	a.layers = []Layer{
		{Name: "Fox", Color: "#ff6b6b", Frames: make([]bool, a.totalFrames+1)},
		{Name: "Foreground", Color: "#ffd166", Frames: make([]bool, a.totalFrames+1)},
		{Name: "Background", Color: "#4dabf7", Frames: make([]bool, a.totalFrames+1)},
	}
	// mark a few keyframes
	for _, f := range []int{1, 15, 30, 45, 60, 90, 120} {
		a.layers[0].Frames[f] = true
	}
	for _, f := range []int{1, 60, 120} {
		a.layers[1].Frames[f] = true
		a.layers[2].Frames[f] = true
	}
}

func (a *App) bindUI() {
	d := js.Global().Get("document")
	w := js.Global().Get("window")

	a.bindMenus()

	// tool buttons
	btns := d.Call("querySelectorAll", ".toolbtn")
	for i := 0; i < btns.Length(); i++ {
		b := btns.Index(i)
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			tool := this.Get("dataset").Get("tool").String()
			a.setActiveTool(tool)
			return nil
		})
		b.Call("addEventListener", "click", cb)
	}

	// publish button (demo)
	d.Call("getElementById", "btn-publish").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			js.Global().Call("alert", "Pretend we exported a build 😄")
			return nil
		}),
	)

	// timeline zoom
	d.Call("getElementById", "btn-zoom").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			a.zoom = math.Min(a.zoom*1.25, 40)
			return nil
		}),
	)
	d.Call("getElementById", "btn-zoom-2").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			a.zoom = math.Max(a.zoom/1.25, 4)
			return nil
		}),
	)

	// add layer
	d.Call("getElementById", "btn-layer").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			n := len(a.layers) + 1
			a.layers = append([]Layer{{Name: fmt.Sprintf("Layer %d", n), Color: "#c77dff", Frames: make([]bool, a.totalFrames+1)}}, a.layers...)
			return nil
		}),
	)

	// window resize
	w.Call("addEventListener", "resize", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.resizeCanvases()
		return nil
	}))

	// keyboard
	d.Call("addEventListener", "keydown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		key := e.Get("key").String()
		if key == " " {
			e.Call("preventDefault")
			a.playing = !a.playing
		}
		if key == "ArrowLeft" {
			a.setFrame(a.curFrame - 1)
		}
		if key == "ArrowRight" {
			a.setFrame(a.curFrame + 1)
		}
		return nil
	}))

	// timeline mouse events
	a.tlCanvas.Call("addEventListener", "mousedown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()

		phX := a.frameToX(a.curFrame)
		if math.Abs(x-phX) < 8 && y > 0 {
			a.draggingPH = true
			a.playing = false
			return nil
		}

		// click to set frame (in grid area)
		if x > a.headerW {
			f := a.xToFrame(x)
			a.setFrame(f)
		}
		return nil
	}))
	a.tlCanvas.Call("addEventListener", "mousemove", js.FuncOf(func(this js.Value, args []js.Value) any {
		if !a.draggingPH {
			return nil
		}
		e := args[0]
		x := e.Get("offsetX").Float()
		a.setFrame(a.xToFrame(x))
		return nil
	}))
	w.Call("addEventListener", "mouseup", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.draggingPH = false
		return nil
	}))
}

func (a *App) setActiveTool(tool string) {
	d := js.Global().Get("document")
	btns := d.Call("querySelectorAll", ".toolbtn")
	for i := 0; i < btns.Length(); i++ {
		b := btns.Index(i)
		if b.Get("dataset").Get("tool").String() == tool {
			b.Get("classList").Call("add", "active")
		} else {
			b.Get("classList").Call("remove", "active")
		}
	}
	// friendly name
	name := map[string]string{
		"select":    "Selection",
		"transform": "Transform",
		"text":      "Text",
		"shape":     "Shape",
		"tween":     "Classic Tween",
		"action":    "Action Script",
	}[tool]
	if name == "" {
		name = tool
	}
	a.selToolEl.Set("textContent", name)
}

func (a *App) resizeCanvases() {
	// stage: match CSS size but set real backing buffer for crispness
	a.resizeCanvasToCSSPixels(a.stageCanvas)
	a.resizeCanvasToCSSPixels(a.tlCanvas)

	// update props
	a.docSizeEl.Set("textContent", fmt.Sprintf("%d × %d px", a.docW, a.docH))
}

func (a *App) resizeCanvasToCSSPixels(canvas js.Value) {
	dpr := js.Global().Get("devicePixelRatio").Float()
	rect := canvas.Call("getBoundingClientRect")
	w := rect.Get("width").Float()
	h := rect.Get("height").Float()
	canvas.Set("width", int(w*dpr))
	canvas.Set("height", int(h*dpr))
	ctx := canvas.Call("getContext", "2d")
	ctx.Call("setTransform", dpr, 0, 0, dpr, 0, 0) // draw in CSS pixels
}

func (a *App) tick() {
	now := time.Now()
	dt := now.Sub(a.lastTick)
	a.lastTick = now

	// update UI labels
	a.curFrameEl.Set("textContent", fmt.Sprintf("%d", a.curFrame))
	if a.playing {
		a.isPlayEl.Set("textContent", "Yes")
	} else {
		a.isPlayEl.Set("textContent", "No")
	}

	if !a.playing {
		return
	}

	advance := float64(dt) / float64(time.Second) * float64(a.fps)
	if advance <= 0 {
		return
	}

	// step at least 1 frame when enough time accumulates
	next := a.curFrame + int(math.Floor(advance))
	if next == a.curFrame {
		next++
	}
	if next > a.totalFrames {
		next = 1
	}
	a.curFrame = next

	// demo stage movement tied to frame
	a.foxX = 120 + float64(a.curFrame)*3.0
}

func (a *App) setFrame(f int) {
	if f < 1 {
		f = 1
	}
	if f > a.totalFrames {
		f = a.totalFrames
	}
	a.curFrame = f
}

func (a *App) frameToX(frame int) float64 {
	return a.headerW + float64(frame-1)*a.zoom
}

func (a *App) xToFrame(x float64) int {
	f := int(math.Round((x-a.headerW)/a.zoom)) + 1
	if f < 1 {
		f = 1
	}
	if f > a.totalFrames {
		f = a.totalFrames
	}
	return f
}

func (a *App) renderAll() {
	a.renderStage()
	a.renderTimeline()
}

func (a *App) renderStage() {
	ctx := a.stageCtx
	w := a.stageCanvas.Get("width").Float() / js.Global().Get("devicePixelRatio").Float()
	h := a.stageCanvas.Get("height").Float() / js.Global().Get("devicePixelRatio").Float()

	// background
	ctx.Set("fillStyle", "#86c5ff")
	ctx.Call("fillRect", 0, 0, w, h)

	// hills
	ctx.Set("fillStyle", "#57a773")
	ctx.Call("fillRect", 0, h*0.72, w, h*0.28)
	ctx.Set("fillStyle", "#3f7f57")
	ctx.Call("fillRect", 0, h*0.82, w, h*0.18)

	// simple sun
	ctx.Set("fillStyle", "#ffd166")
	ctx.Call("beginPath")
	ctx.Call("arc", w*0.82, h*0.22, 28, 0, math.Pi*2)
	ctx.Call("fill")

	// "fox" placeholder
	x := math.Mod(a.foxX, w+60) - 30
	y := h * 0.65

	ctx.Set("fillStyle", "#ff5a5f")
	ctx.Call("fillRect", x-18, y-22, 36, 24) // body
	ctx.Set("fillStyle", "#111")
	ctx.Call("fillRect", x+6, y-18, 4, 4) // eye
	ctx.Set("fillStyle", "#fff")
	ctx.Call("fillRect", x+14, y-10, 10, 6) // snout highlight

	// stage border vibe
	ctx.Set("strokeStyle", "rgba(0,0,0,0.25)")
	ctx.Set("lineWidth", 2)
	ctx.Call("strokeRect", 1, 1, w-2, h-2)
}

func (a *App) renderTimeline() {
	ctx := a.tlCtx
	w := a.tlCanvas.Get("width").Float() / js.Global().Get("devicePixelRatio").Float()
	h := a.tlCanvas.Get("height").Float() / js.Global().Get("devicePixelRatio").Float()

	// background
	ctx.Set("fillStyle", "#1a1c1f")
	ctx.Call("fillRect", 0, 0, w, h)

	// header panel (layers)
	ctx.Set("fillStyle", "#23262a")
	ctx.Call("fillRect", 0, 0, a.headerW, h)
	ctx.Set("strokeStyle", "#3a3f45")
	ctx.Call("beginPath")
	ctx.Call("moveTo", a.headerW+0.5, 0)
	ctx.Call("lineTo", a.headerW+0.5, h)
	ctx.Call("stroke")

	// frame grid
	topPad := 10.0
	for f := 1; f <= a.totalFrames; f++ {
		x := a.frameToX(f)
		if x < a.headerW || x > w {
			continue
		}
		// major tick every 10
		if f%10 == 1 {
			ctx.Set("strokeStyle", "rgba(255,255,255,0.12)")
		} else {
			ctx.Set("strokeStyle", "rgba(255,255,255,0.06)")
		}
		ctx.Call("beginPath")
		ctx.Call("moveTo", x+0.5, 0)
		ctx.Call("lineTo", x+0.5, h)
		ctx.Call("stroke")

		// labels
		if f%10 == 1 {
			ctx.Set("fillStyle", "rgba(255,255,255,0.55)")
			ctx.Set("font", "12px system-ui")
			ctx.Call("fillText", fmt.Sprintf("%d", f), x+2, 14)
		}
	}

	// layers rows
	for i, layer := range a.layers {
		y := topPad + float64(i)*a.layerH + 22
		// row background
		if i%2 == 0 {
			ctx.Set("fillStyle", "rgba(255,255,255,0.02)")
		} else {
			ctx.Set("fillStyle", "rgba(0,0,0,0.0)")
		}
		ctx.Call("fillRect", 0, y-18, w, a.layerH)

		// layer name
		ctx.Set("fillStyle", "rgba(255,255,255,0.85)")
		ctx.Set("font", "13px system-ui")
		ctx.Call("fillText", layer.Name, 12, y)

		// keyframes
		for f := 1; f <= a.totalFrames; f++ {
			if !layer.Frames[f] {
				continue
			}
			x := a.frameToX(f)
			if x < a.headerW || x > w {
				continue
			}
			ctx.Set("fillStyle", layer.Color)
			ctx.Call("fillRect", x+2, y-10, math.Max(6, a.zoom-4), 14)
		}
	}

	// playhead
	phX := a.frameToX(a.curFrame)
	ctx.Set("strokeStyle", "#ff3b3b")
	ctx.Set("lineWidth", 2)
	ctx.Call("beginPath")
	ctx.Call("moveTo", phX+0.5, 0)
	ctx.Call("lineTo", phX+0.5, h)
	ctx.Call("stroke")

	// playhead top marker
	ctx.Set("fillStyle", "#ff3b3b")
	ctx.Call("beginPath")
	ctx.Call("moveTo", phX, 0)
	ctx.Call("lineTo", phX-8, 16)
	ctx.Call("lineTo", phX+8, 16)
	ctx.Call("closePath")
	ctx.Call("fill")
}

func (a *App) bindMenus() {
	d := js.Global().Get("document")
	w := js.Global().Get("window")

	// Top-level menu buttons
	menuBtns := d.Call("querySelectorAll", ".menuBtn")
	for i := 0; i < menuBtns.Length(); i++ {
		btn := menuBtns.Index(i)
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("stopPropagation")
			}
			menuName := this.Get("dataset").Get("menu").String()
			a.toggleMenu(menuName)
			return nil
		})
		btn.Call("addEventListener", "click", cb)

		hoverCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if a.activeMenu == "" {
				return nil
			}
			menuName := this.Get("dataset").Get("menu").String()
			if menuName != a.activeMenu {
				a.openMenu(menuName)
			}
			return nil
		})
		btn.Call("addEventListener", "mouseenter", hoverCb)
	}

	// Menu items
	items := d.Call("querySelectorAll", ".menuItem")
	for i := 0; i < items.Length(); i++ {
		item := items.Index(i)
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			event := args[0]
			target := event.Get("target")
			// Button label shown in HTML, e.g. <button>Save</button>
			buttonName := target.Get("id").String()
			fmt.Println(buttonName)

			// switch buttonName {
			// case "New":
			// 	fmt.Println("New")
			// case "Open…":
			// 	fmt.Println("Open…")
			// case "Save":
			// 	fmt.Println("Save")
			// case "Export":
			// 	fmt.Println("Export")
			// }

			if len(args) > 0 {
				args[0].Call("stopPropagation")
			}

			action := this.Get("dataset").Get("action").String()
			a.closeMenus()
			a.handleMenuAction(action)
			return nil
		})
		item.Call("addEventListener", "click", cb)
	}

	// Click outside closes menus
	d.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeMenus()
		return nil
	}))

	// Escape closes menus
	d.Call("addEventListener", "keydown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		if e.Get("key").String() == "Escape" {
			a.closeMenus()
		}
		return nil
	}))

	// Window blur closes menus too
	w.Call("addEventListener", "blur", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeMenus()
		return nil
	}))
}

func (a *App) toggleMenu(name string) {
	if a.activeMenu == name {
		a.closeMenus()
		return
	}
	a.openMenu(name)
}

func (a *App) openMenu(name string) {
	a.closeMenus()

	d := js.Global().Get("document")

	btn := d.Call("querySelector", fmt.Sprintf(`.menuBtn[data-menu="%s"]`, name))
	dd := d.Call("querySelector", fmt.Sprintf(`.dropdown[data-dropdown="%s"]`, name))

	if btn.Truthy() {
		btn.Get("classList").Call("add", "open")
	}
	if dd.Truthy() {
		dd.Get("classList").Call("add", "open")
	}

	a.activeMenu = name
}

func (a *App) closeMenus() {
	d := js.Global().Get("document")

	btns := d.Call("querySelectorAll", ".menuBtn.open")
	for i := 0; i < btns.Length(); i++ {
		btns.Index(i).Get("classList").Call("remove", "open")
	}

	dds := d.Call("querySelectorAll", ".dropdown.open")
	for i := 0; i < dds.Length(); i++ {
		dds.Index(i).Get("classList").Call("remove", "open")
	}

	a.activeMenu = ""
}

func (a *App) handleMenuAction(action string) {
	switch action {

	case "file.new":
		a.setFrame(1)
		a.playing = false
		a.statusEl.Set("textContent", "New document")

	case "file.open":
		a.statusEl.Set("textContent", "Open requested")

	case "file.save":
		a.statusEl.Set("textContent", "Save requested")

	case "file.export":
		a.statusEl.Set("textContent", "Export requested")
		js.Global().Call("alert", "Export hook clicked")

	case "edit.undo":
		a.statusEl.Set("textContent", "Undo requested")

	case "edit.redo":
		a.statusEl.Set("textContent", "Redo requested")

	case "edit.cut":
		a.statusEl.Set("textContent", "Cut requested")

	case "edit.copy":
		a.statusEl.Set("textContent", "Copy requested")

	case "edit.paste":
		a.statusEl.Set("textContent", "Paste requested")

	case "view.zoomIn":
		a.zoom = math.Min(a.zoom*1.25, 40)
		a.statusEl.Set("textContent", "Timeline zoomed in")

	case "view.zoomOut":
		a.zoom = math.Max(a.zoom/1.25, 4)
		a.statusEl.Set("textContent", "Timeline zoomed out")

	case "view.resetZoom":
		a.zoom = 10
		a.statusEl.Set("textContent", "Zoom reset")

	case "insert.layer":
		n := len(a.layers) + 1
		a.layers = append([]Layer{{
			Name:   fmt.Sprintf("Layer %d", n),
			Color:  "#c77dff",
			Frames: make([]bool, a.totalFrames+1),
		}}, a.layers...)
		a.statusEl.Set("textContent", "Layer added")

	case "insert.keyframe":
		if len(a.layers) > 0 {
			a.layers[0].Frames[a.curFrame] = true
		}
		a.statusEl.Set("textContent", fmt.Sprintf("Keyframe added at %d", a.curFrame))

	case "insert.blankKeyframe":
		if len(a.layers) > 0 {
			a.layers[0].Frames[a.curFrame] = true
		}
		a.statusEl.Set("textContent", fmt.Sprintf("Blank keyframe hook at %d", a.curFrame))

	case "modify.convertToSymbol":
		a.statusEl.Set("textContent", "Convert to Symbol requested")

	case "modify.breakApart":
		a.statusEl.Set("textContent", "Break Apart requested")

	case "text.bold":
		a.statusEl.Set("textContent", "Bold requested")

	case "text.italic":
		a.statusEl.Set("textContent", "Italic requested")

	case "commands.testAlert":
		a.statusEl.Set("textContent", "Test command executed")
		js.Global().Call("alert", "Menu hook is wired and alive")

	case "control.play":
		a.playing = true
		a.statusEl.Set("textContent", "Playback started")

	case "control.stop":
		a.playing = false
		a.statusEl.Set("textContent", "Playback stopped")

	case "control.rewind":
		a.playing = false
		a.setFrame(1)
		a.statusEl.Set("textContent", "Rewound to frame 1")

	case "window.properties":
		a.statusEl.Set("textContent", "Properties panel hook clicked")

	case "window.library":
		a.statusEl.Set("textContent", "Library panel hook clicked")

	case "help.about":
		js.Global().Call("alert", "Animate-like Editor\nBuilt with Go + WASM")
		a.statusEl.Set("textContent", "About opened")

	default:
		a.statusEl.Set("textContent", "Unhandled action: "+action)
	}
}
