//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"syscall/js"
	"time"
)

type Layer struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Color       string            `json:"color"`
	Instances   []ElementInstance `json:"instances"`
}

type ElementInstance struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Keyframes   map[int]InstanceKeyframe `json:"keyframes"` // frame -> animation data
}

type InstanceKeyframe struct {
	Frame    int     `json:"frame"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	ScaleX   float64 `json:"scaleX"`
	ScaleY   float64 `json:"scaleY"`
	Rotation float64 `json:"rotation"`
	Opacity  float64 `json:"opacity"`
}

type Document struct {
	Name        string  `json:"name"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	FPS         int     `json:"fps"`
	TotalFrames int     `json:"totalFrames"`
	Layers      []Layer `json:"layers"`
}

func newDefaultDocument() Document {
	doc := Document{
		Name:        "scene-1",
		Width:       640,
		Height:      360,
		FPS:         24,
		TotalFrames: 120,
		Layers: []Layer{
			{
				Name:        "Fox",
				Description: "Main fox character layer",
				Color:       "#ff6b6b",
				Instances: []ElementInstance{{
					ID:          "fox-instance-1",
					Name:        "Fox Symbol",
					Description: "Primary fox instance on stage",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			},
			{
				Name:        "Foreground",
				Description: "Foreground decorative elements",
				Color:       "#ffd166",
				Instances: []ElementInstance{{
					ID:          "foreground-instance-1",
					Name:        "Foreground Group",
					Description: "Foreground grouped elements",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			},
			{
				Name:        "Background",
				Description: "Background elements",
				Color:       "#4dabf7",
				Instances: []ElementInstance{{
					ID:          "background-instance-1",
					Name:        "Background Group",
					Description: "Background grouped elements",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			},
		},
	}

	// mark a few keyframes
	for _, f := range []int{1, 15, 30, 45, 60, 90, 120} {
		doc.Layers[0].Instances[0].Keyframes[f] = defaultKeyframeAt(f)
	}
	for _, f := range []int{1, 60, 120} {
		doc.Layers[1].Instances[0].Keyframes[f] = defaultKeyframeAt(f)
		doc.Layers[2].Instances[0].Keyframes[f] = defaultKeyframeAt(f)
	}

	return doc
}

func defaultKeyframeAt(frame int) InstanceKeyframe {
	return InstanceKeyframe{
		Frame:    frame,
		X:        0,
		Y:        0,
		ScaleX:   1,
		ScaleY:   1,
		Rotation: 0,
		Opacity:  1,
	}
}

func (l *Layer) hasKeyframe(frame int) bool {
	for _, inst := range l.Instances {
		if _, ok := inst.Keyframes[frame]; ok {
			return true
		}
	}
	return false
}

func (a *App) addKeyframe(layerIdx, instanceIdx, frame int) {
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return
	}
	if instanceIdx < 0 || instanceIdx >= len(a.doc.Layers[layerIdx].Instances) {
		return
	}
	inst := &a.doc.Layers[layerIdx].Instances[instanceIdx]
	inst.Keyframes[frame] = defaultKeyframeAt(frame)
}

type App struct {
	activeMenu string

	doc Document

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

	// timeline state
	curFrame int // 1-based
	playing  bool

	zoom       float64 // pixels per frame
	layerH     float64
	headerW    float64
	playheadX  float64
	draggingPH bool

	lastTick time.Time

	// stage demo
	foxX float64

	heldCallbacks []js.Func
}

func (a *App) holdCallback(fn js.Func) js.Func {
	a.heldCallbacks = append(a.heldCallbacks, fn)
	return fn
}

func main() {
	app := &App{
		doc:      newDefaultDocument(),
		curFrame: 1,
		zoom:     10,  // px per frame
		layerH:   28,  // px
		headerW:  180, // px
		foxX:     120, // demo actor x
	}

	app.initDOM()
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
	a.refreshDocUI()
}

func (a *App) refreshDocUI() {
	a.docSizeEl.Set("textContent", fmt.Sprintf("%d x %d px", a.doc.Width, a.doc.Height))
	a.docFpsEl.Set("textContent", fmt.Sprintf("%d", a.doc.FPS))
}

func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "document"
	}
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	clean := strings.Trim(b.String(), "-.")
	if clean == "" {
		return "document"
	}
	return clean
}

func normalizeDocument(doc *Document) {
	if doc.Name == "" {
		doc.Name = "scene-1"
	}
	if doc.Width <= 0 {
		doc.Width = 640
	}
	if doc.Height <= 0 {
		doc.Height = 360
	}
	if doc.FPS <= 0 {
		doc.FPS = 24
	}
	if doc.TotalFrames <= 0 {
		doc.TotalFrames = 120
	}

	for li := range doc.Layers {
		layer := &doc.Layers[li]
		if layer.Color == "" {
			layer.Color = "#c77dff"
		}
		if len(layer.Instances) == 0 {
			layer.Instances = []ElementInstance{{
				ID:          fmt.Sprintf("layer-%d-instance-1", li+1),
				Name:        "Symbol Instance",
				Description: "Default element instance",
				Keyframes:   make(map[int]InstanceKeyframe),
			}}
		}
		for ii := range layer.Instances {
			inst := &layer.Instances[ii]
			if inst.ID == "" {
				inst.ID = fmt.Sprintf("layer-%d-instance-%d", li+1, ii+1)
			}
			if inst.Name == "" {
				inst.Name = "Symbol Instance"
			}
			if inst.Keyframes == nil {
				inst.Keyframes = make(map[int]InstanceKeyframe)
			}

			for frame, kf := range inst.Keyframes {
				if frame < 1 || frame > doc.TotalFrames {
					delete(inst.Keyframes, frame)
					continue
				}
				if kf.Frame == 0 {
					kf.Frame = frame
				}
				if kf.ScaleX == 0 {
					kf.ScaleX = 1
				}
				if kf.ScaleY == 0 {
					kf.ScaleY = 1
				}
				if kf.Opacity < 0 || kf.Opacity > 1 {
					kf.Opacity = 1
				}
				inst.Keyframes[frame] = kf
			}
		}
	}
}

func (a *App) loadDocumentJSONText(text string) error {
	var doc Document
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		return err
	}
	normalizeDocument(&doc)
	a.doc = doc
	a.setFrame(a.curFrame)
	a.refreshDocUI()
	a.renderAll()
	return nil
}

func (a *App) saveDocumentToDisk() error {
	data, err := json.MarshalIndent(a.doc, "", "  ")
	if err != nil {
		return err
	}

	u8 := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(u8, data)

	blob := js.Global().Get("Blob").New([]any{u8}, map[string]any{
		"type": "application/json",
	})
	url := js.Global().Get("URL").Call("createObjectURL", blob)

	d := js.Global().Get("document")
	aEl := d.Call("createElement", "a")
	aEl.Set("href", url)
	aEl.Set("download", sanitizeFileName(a.doc.Name)+".json")

	d.Get("body").Call("appendChild", aEl)
	aEl.Call("click")
	d.Get("body").Call("removeChild", aEl)
	js.Global().Get("URL").Call("revokeObjectURL", url)
	return nil
}

func (a *App) openDocumentFromDisk() error {
	d := js.Global().Get("document")
	input := d.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("accept", ".json,application/json")

	changeCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		files := input.Get("files")
		if !files.Truthy() || files.Length() == 0 {
			return nil
		}

		file := files.Index(0)
		thenCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			text := args[0].String()
			if err := a.loadDocumentJSONText(text); err != nil {
				a.statusEl.Set("textContent", "Open failed: "+err.Error())
				return nil
			}
			a.statusEl.Set("textContent", "Opened "+file.Get("name").String())
			return nil
		})
		catchCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			msg := "Unknown error"
			if len(args) > 0 {
				msg = args[0].String()
			}
			a.statusEl.Set("textContent", "Open failed: "+msg)
			return nil
		})
		a.holdCallback(thenCb)
		a.holdCallback(catchCb)
		file.Call("text").Call("then", thenCb).Call("catch", catchCb)
		return nil
	})

	a.holdCallback(changeCb)
	input.Call("addEventListener", "change", changeCb)
	input.Call("click")
	return nil
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
			js.Global().Call("alert", "Pretend we exported a build :)")
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
			n := len(a.doc.Layers) + 1
			a.doc.Layers = append([]Layer{{
				Name:        fmt.Sprintf("Layer %d", n),
				Description: fmt.Sprintf("User created layer %d", n),
				Color:       "#c77dff",
				Instances: []ElementInstance{{
					ID:          fmt.Sprintf("layer-%d-instance-1", n),
					Name:        "Symbol Instance",
					Description: "Default element instance",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			}}, a.doc.Layers...)
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
	a.docSizeEl.Set("textContent", fmt.Sprintf("%d x %d px", a.doc.Width, a.doc.Height))
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

	advance := float64(dt) / float64(time.Second) * float64(a.doc.FPS)
	if advance <= 0 {
		return
	}

	// step at least 1 frame when enough time accumulates
	next := a.curFrame + int(math.Floor(advance))
	if next == a.curFrame {
		next++
	}
	if next > a.doc.TotalFrames {
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
	if f > a.doc.TotalFrames {
		f = a.doc.TotalFrames
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
	if f > a.doc.TotalFrames {
		f = a.doc.TotalFrames
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
	for f := 1; f <= a.doc.TotalFrames; f++ {
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
	for i, layer := range a.doc.Layers {
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
		for f := 1; f <= a.doc.TotalFrames; f++ {
			if !layer.hasKeyframe(f) {
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
		a.doc = newDefaultDocument()
		a.setFrame(1)
		a.playing = false
		a.refreshDocUI()
		a.statusEl.Set("textContent", "New document")

	case "file.open":
		if err := a.openDocumentFromDisk(); err != nil {
			a.statusEl.Set("textContent", "Open failed: "+err.Error())
			return
		}
		a.statusEl.Set("textContent", "Choose a .json document to open")

	case "file.save":
		if err := a.saveDocumentToDisk(); err != nil {
			a.statusEl.Set("textContent", "Save failed: "+err.Error())
			return
		}
		a.statusEl.Set("textContent", "Document downloaded as JSON")

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
		n := len(a.doc.Layers) + 1
		a.doc.Layers = append([]Layer{{
			Name:        fmt.Sprintf("Layer %d", n),
			Description: fmt.Sprintf("User created layer %d", n),
			Color:       "#c77dff",
			Instances: []ElementInstance{{
				ID:          fmt.Sprintf("layer-%d-instance-1", n),
				Name:        "Symbol Instance",
				Description: "Default element instance",
				Keyframes:   make(map[int]InstanceKeyframe),
			}},
		}}, a.doc.Layers...)
		a.statusEl.Set("textContent", "Layer added")

	case "insert.keyframe":
		if len(a.doc.Layers) > 0 && len(a.doc.Layers[0].Instances) > 0 {
			a.addKeyframe(0, 0, a.curFrame)
		}
		a.statusEl.Set("textContent", fmt.Sprintf("Keyframe added at %d", a.curFrame))

	case "insert.blankKeyframe":
		if len(a.doc.Layers) > 0 && len(a.doc.Layers[0].Instances) > 0 {
			a.addKeyframe(0, 0, a.curFrame)
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
