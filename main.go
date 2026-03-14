//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"syscall/js"
	"time"
)

type Layer struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Color       string            `json:"color"`
	Selected    bool              `json:"selected"`
	Instances   []ElementInstance `json:"instances"`
}

type ElementInstance struct {
	ID          string                   `json:"id"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	ElementType string                   `json:"elementType,omitempty"`
	ElementID   string                   `json:"elementId,omitempty"`
	Keyframes   map[int]InstanceKeyframe `json:"keyframes"` // frame -> animation data
}

type InstanceKeyframe struct {
	Frame    int     `json:"frame"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	ScaleX   float64 `json:"scaleX"`
	ScaleY   float64 `json:"scaleY"`
	SkewX    float64 `json:"skewX"`
	SkewY    float64 `json:"skewY"`
	Rotation float64 `json:"rotation"`
	AnchorX  float64 `json:"anchorX"`
	AnchorY  float64 `json:"anchorY"`
	Opacity  float64 `json:"opacity"`
}

type BezierPoint struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	InX  float64 `json:"inX"`
	InY  float64 `json:"inY"`
	OutX float64 `json:"outX"`
	OutY float64 `json:"outY"`
}

type VectorCircle struct {
	ID        string  `json:"id"`
	CX        float64 `json:"cx"`
	CY        float64 `json:"cy"`
	Radius    float64 `json:"radius"`
	Fill      string  `json:"fill"`
	Stroke    string  `json:"stroke"`
	StrokeW   float64 `json:"strokeW"`
	LayerName string  `json:"layerName,omitempty"`
}

type VectorPath struct {
	ID      string        `json:"id"`
	Points  []BezierPoint `json:"points"`
	Stroke  string        `json:"stroke"`
	Fill    string        `json:"fill"`
	StrokeW float64       `json:"strokeW"`
	Closed  bool          `json:"closed"`
}

type Document struct {
	Name        string         `json:"name"`
	Width       int            `json:"width"`
	Height      int            `json:"height"`
	FPS         int            `json:"fps"`
	TotalFrames int            `json:"totalFrames"`
	Layers      []Layer        `json:"layers"`
	Circles     []VectorCircle `json:"circles"`
	Paths       []VectorPath   `json:"paths"`
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
				Selected:    true,
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
				Selected:    false,
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
				Selected:    false,
				Instances: []ElementInstance{{
					ID:          "background-instance-1",
					Name:        "Background Group",
					Description: "Background grouped elements",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			},
		},
		Circles: []VectorCircle{},
		Paths:   []VectorPath{},
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
		SkewX:    0,
		SkewY:    0,
		Rotation: 0,
		AnchorX:  0,
		AnchorY:  0,
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

type mat2d struct {
	a, b, c, d, e, f float64
}

func matIdentity() mat2d { return mat2d{a: 1, d: 1} }

func matMul(m1, m2 mat2d) mat2d {
	return mat2d{
		a: m1.a*m2.a + m1.c*m2.b,
		b: m1.b*m2.a + m1.d*m2.b,
		c: m1.a*m2.c + m1.c*m2.d,
		d: m1.b*m2.c + m1.d*m2.d,
		e: m1.a*m2.e + m1.c*m2.f + m1.e,
		f: m1.b*m2.e + m1.d*m2.f + m1.f,
	}
}

func matTranslate(x, y float64) mat2d { return mat2d{a: 1, d: 1, e: x, f: y} }
func matScale(x, y float64) mat2d     { return mat2d{a: x, d: y} }
func matRotate(rad float64) mat2d {
	c := math.Cos(rad)
	s := math.Sin(rad)
	return mat2d{a: c, b: s, c: -s, d: c}
}
func matSkew(sx, sy float64) mat2d {
	return mat2d{a: 1, b: math.Tan(sy), c: math.Tan(sx), d: 1}
}

func matApply(m mat2d, x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

func matInvert(m mat2d) (mat2d, bool) {
	det := m.a*m.d - m.b*m.c
	if math.Abs(det) < 1e-9 {
		return mat2d{}, false
	}
	id := 1.0 / det
	return mat2d{
		a: m.d * id,
		b: -m.b * id,
		c: -m.c * id,
		d: m.a * id,
		e: (m.c*m.f - m.d*m.e) * id,
		f: (m.b*m.e - m.a*m.f) * id,
	}, true
}

func instanceMatrix(kf InstanceKeyframe) mat2d {
	m := matIdentity()
	m = matMul(m, matTranslate(kf.X, kf.Y))
	m = matMul(m, matTranslate(kf.AnchorX, kf.AnchorY))
	m = matMul(m, matRotate(kf.Rotation))
	m = matMul(m, matSkew(kf.SkewX, kf.SkewY))
	m = matMul(m, matScale(kf.ScaleX, kf.ScaleY))
	m = matMul(m, matTranslate(-kf.AnchorX, -kf.AnchorY))
	return m
}

type App struct {
	activeMenu string
	activeTool string

	doc Document

	stageCanvas js.Value
	stageCtx    js.Value

	tlCanvas js.Value
	tlCtx    js.Value

	statusEl    js.Value
	docSizeEl   js.Value
	docFpsEl    js.Value
	curFrameEl  js.Value
	isPlayEl    js.Value
	selNameEl   js.Value
	selToolEl   js.Value
	propPosX    js.Value
	propPosY    js.Value
	propScaleX  js.Value
	propScaleY  js.Value
	propSkewX   js.Value
	propSkewY   js.Value
	propRot     js.Value
	propRotDec  js.Value
	propRotInc  js.Value
	propAncX    js.Value
	propAncY    js.Value
	propFill    js.Value
	propStroke  js.Value
	propStrokeW js.Value

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

	drawingCircle bool
	circleStartX  float64
	circleStartY  float64
	circleNowX    float64
	circleNowY    float64

	penEditing   bool
	penPoints    []BezierPoint
	penMouseDown bool
	penDragIndex int
	penDragMoved bool
	penMouseX    float64
	penMouseY    float64

	selectedLayerIdx  int
	selectedInstIdx   int
	selectedInstances map[string]bool
	selectedPathPt    int
	selectedHandle    string
	dragMode          string
	lastMouseX        float64
	lastMouseY        float64
	marqueeActive     bool
	marqueeStartX     float64
	marqueeStartY     float64
	marqueeNowX       float64
	marqueeNowY       float64
	marqueeAdditive   bool

	heldCallbacks []js.Func
}

func (a *App) holdCallback(fn js.Func) js.Func {
	a.heldCallbacks = append(a.heldCallbacks, fn)
	return fn
}

func main() {
	app := &App{
		doc:               newDefaultDocument(),
		activeTool:        "select",
		curFrame:          1,
		zoom:              10,  // px per frame
		layerH:            28,  // px
		headerW:           180, // px
		foxX:              120, // demo actor x
		selectedLayerIdx:  -1,
		selectedInstIdx:   -1,
		selectedInstances: make(map[string]bool),
		selectedPathPt:    -1,
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
	a.selNameEl = d.Call("getElementById", "selName")
	a.selToolEl = d.Call("getElementById", "selTool")
	a.propPosX = d.Call("getElementById", "propPosX")
	a.propPosY = d.Call("getElementById", "propPosY")
	a.propScaleX = d.Call("getElementById", "propScaleX")
	a.propScaleY = d.Call("getElementById", "propScaleY")
	a.propSkewX = d.Call("getElementById", "propSkewX")
	a.propSkewY = d.Call("getElementById", "propSkewY")
	a.propRot = d.Call("getElementById", "propRot")
	a.propRotDec = d.Call("getElementById", "propRotDec")
	a.propRotInc = d.Call("getElementById", "propRotInc")
	a.propAncX = d.Call("getElementById", "propAncX")
	a.propAncY = d.Call("getElementById", "propAncY")
	a.propFill = d.Call("getElementById", "propFill")
	a.propStroke = d.Call("getElementById", "propStroke")
	a.propStrokeW = d.Call("getElementById", "propStrokeW")

	a.statusEl.Set("textContent", "WASM ready")
	a.refreshDocUI()
}

func (a *App) refreshDocUI() {
	a.docSizeEl.Set("textContent", fmt.Sprintf("%d x %d px", a.doc.Width, a.doc.Height))
	a.docFpsEl.Set("textContent", fmt.Sprintf("%d", a.doc.FPS))
	a.updateSelectedLayerLabel()
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
	anySelected := false
	for _, l := range doc.Layers {
		if l.Selected {
			anySelected = true
			break
		}
	}
	if !anySelected && len(doc.Layers) > 0 {
		doc.Layers[0].Selected = true
	}

	for ci := range doc.Circles {
		c := &doc.Circles[ci]
		if c.ID == "" {
			c.ID = fmt.Sprintf("circle-%d", ci+1)
		}
		if c.Radius < 0 {
			c.Radius = -c.Radius
		}
		if c.Fill == "" {
			c.Fill = "rgba(255, 204, 102, 0.35)"
		}
		if c.Stroke == "" {
			c.Stroke = "#ffcc66"
		}
		if c.StrokeW <= 0 {
			c.StrokeW = 2
		}
	}

	for pi := range doc.Paths {
		p := &doc.Paths[pi]
		if p.ID == "" {
			p.ID = fmt.Sprintf("path-%d", pi+1)
		}
		if p.Stroke == "" {
			p.Stroke = "#66e3ff"
		}
		if p.StrokeW <= 0 {
			p.StrokeW = 2
		}
		if p.Closed {
			if p.Fill == "" {
				p.Fill = "rgba(102, 227, 255, 0.25)"
			}
		} else if p.Fill == "" {
			p.Fill = "transparent"
		}
		for i := range p.Points {
			pt := &p.Points[i]
			// Backward-compatible default: if handles are missing, use corner point.
			if pt.InX == 0 && pt.InY == 0 && pt.OutX == 0 && pt.OutY == 0 {
				pt.InX = pt.X
				pt.InY = pt.Y
				pt.OutX = pt.X
				pt.OutY = pt.Y
			}
		}
	}
}

func (a *App) nextCircleID() string {
	return fmt.Sprintf("circle-%d", len(a.doc.Circles)+1)
}

func (a *App) nextPathID() string {
	return fmt.Sprintf("path-%d", len(a.doc.Paths)+1)
}

func (a *App) selectedLayerIndexes() []int {
	selected := make([]int, 0, len(a.doc.Layers))
	for i := range a.doc.Layers {
		if a.doc.Layers[i].Selected {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 && len(a.doc.Layers) > 0 {
		a.doc.Layers[0].Selected = true
		selected = append(selected, 0)
	}
	return selected
}

func (a *App) updateSelectedLayerLabel() {
	pairs := a.selectedInstancePairs()
	if len(pairs) == 1 {
		li, ii := pairs[0][0], pairs[0][1]
		a.selNameEl.Set("textContent", a.doc.Layers[li].Instances[ii].Name)
		return
	}
	if len(pairs) > 1 {
		a.selNameEl.Set("textContent", fmt.Sprintf("%d instances", len(pairs)))
		return
	}
	selected := a.selectedLayerIndexes()
	if len(selected) == 0 {
		a.selNameEl.Set("textContent", "None")
		return
	}
	names := make([]string, 0, len(selected))
	for _, idx := range selected {
		names = append(names, a.doc.Layers[idx].Name)
	}
	a.selNameEl.Set("textContent", strings.Join(names, ", "))
}

func (a *App) selectLayer(layerIdx int, additive bool) {
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return
	}
	if additive {
		a.doc.Layers[layerIdx].Selected = !a.doc.Layers[layerIdx].Selected
	} else {
		for i := range a.doc.Layers {
			a.doc.Layers[i].Selected = i == layerIdx
		}
	}
	a.updateSelectedLayerLabel()
}

func (a *App) addPathInstanceToSelectedLayers(pathID string, baseKeyframe InstanceKeyframe) {
	selected := a.selectedLayerIndexes()
	for _, layerIdx := range selected {
		layer := &a.doc.Layers[layerIdx]
		n := len(layer.Instances) + 1
		inst := ElementInstance{
			ID:          fmt.Sprintf("layer-%d-path-instance-%d", layerIdx+1, n),
			Name:        fmt.Sprintf("Path %d", n),
			Description: "Pen path instance",
			ElementType: "path",
			ElementID:   pathID,
			Keyframes:   make(map[int]InstanceKeyframe),
		}
		baseKeyframe.Frame = a.curFrame
		inst.Keyframes[a.curFrame] = baseKeyframe
		layer.Instances = append([]ElementInstance{inst}, layer.Instances...)
	}
}

func (a *App) addCircleInstanceToSelectedLayers(circleID string, baseKeyframe InstanceKeyframe) {
	selected := a.selectedLayerIndexes()
	for _, layerIdx := range selected {
		layer := &a.doc.Layers[layerIdx]
		n := len(layer.Instances) + 1
		inst := ElementInstance{
			ID:          fmt.Sprintf("layer-%d-circle-instance-%d", layerIdx+1, n),
			Name:        fmt.Sprintf("Circle %d", n),
			Description: "Circle shape instance",
			ElementType: "circle",
			ElementID:   circleID,
			Keyframes:   make(map[int]InstanceKeyframe),
		}
		baseKeyframe.Frame = a.curFrame
		inst.Keyframes[a.curFrame] = baseKeyframe
		layer.Instances = append([]ElementInstance{inst}, layer.Instances...)
	}
}

func (a *App) getInstanceKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return InstanceKeyframe{}, false
	}
	layer := a.doc.Layers[layerIdx]
	if instIdx < 0 || instIdx >= len(layer.Instances) {
		return InstanceKeyframe{}, false
	}
	inst := layer.Instances[instIdx]
	found := false
	best := -1
	for f := range inst.Keyframes {
		if f <= frame && f > best {
			best = f
			found = true
		}
	}
	if !found {
		return InstanceKeyframe{}, false
	}
	kf := inst.Keyframes[best]
	kf.Frame = frame
	return kf, true
}

func (a *App) getOrCreateInstanceKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return InstanceKeyframe{}, false
	}
	if instIdx < 0 || instIdx >= len(a.doc.Layers[layerIdx].Instances) {
		return InstanceKeyframe{}, false
	}
	inst := &a.doc.Layers[layerIdx].Instances[instIdx]
	if existing, ok := inst.Keyframes[frame]; ok {
		return existing, true
	} else if base, ok := a.getInstanceKeyframe(layerIdx, instIdx, frame); ok {
		base.Frame = frame
		inst.Keyframes[frame] = base
		return base, true
	} else {
		kf := defaultKeyframeAt(frame)
		inst.Keyframes[frame] = kf
		return kf, true
	}
}

func (a *App) setInstanceKeyframe(layerIdx, instIdx, frame int, kf InstanceKeyframe) bool {
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return false
	}
	if instIdx < 0 || instIdx >= len(a.doc.Layers[layerIdx].Instances) {
		return false
	}
	kf.Frame = frame
	a.doc.Layers[layerIdx].Instances[instIdx].Keyframes[frame] = kf
	return true
}

func (a *App) clearInstanceSelection() {
	a.selectedLayerIdx = -1
	a.selectedInstIdx = -1
	a.selectedInstances = make(map[string]bool)
	a.selectedPathPt = -1
	a.selectedHandle = ""
}

func selKey(layerIdx, instIdx int) string {
	return fmt.Sprintf("%d:%d", layerIdx, instIdx)
}

func parseSelKey(key string) (int, int, bool) {
	var li, ii int
	if _, err := fmt.Sscanf(key, "%d:%d", &li, &ii); err != nil {
		return 0, 0, false
	}
	return li, ii, true
}

func (a *App) isInstanceSelected(layerIdx, instIdx int) bool {
	return a.selectedInstances[selKey(layerIdx, instIdx)]
}

func (a *App) selectedInstancePairs() [][2]int {
	out := make([][2]int, 0, len(a.selectedInstances))
	for key := range a.selectedInstances {
		li, ii, ok := parseSelKey(key)
		if !ok {
			continue
		}
		if li < 0 || li >= len(a.doc.Layers) || ii < 0 || ii >= len(a.doc.Layers[li].Instances) {
			continue
		}
		out = append(out, [2]int{li, ii})
	}
	return out
}

func (a *App) selectedInstancePairsOrPrimary() [][2]int {
	pairs := a.selectedInstancePairs()
	if len(pairs) > 0 {
		return pairs
	}
	if a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0 {
		return [][2]int{{a.selectedLayerIdx, a.selectedInstIdx}}
	}
	return nil
}

func (a *App) setPrimarySelection(layerIdx, instIdx int) {
	a.selectedLayerIdx = layerIdx
	a.selectedInstIdx = instIdx
}

func (a *App) setSingleInstanceSelection(layerIdx, instIdx int) {
	a.selectedInstances = make(map[string]bool)
	a.selectedInstances[selKey(layerIdx, instIdx)] = true
	a.setPrimarySelection(layerIdx, instIdx)
}

func (a *App) toggleInstanceSelection(layerIdx, instIdx int) {
	key := selKey(layerIdx, instIdx)
	if a.selectedInstances[key] {
		delete(a.selectedInstances, key)
		if a.selectedLayerIdx == layerIdx && a.selectedInstIdx == instIdx {
			a.selectedLayerIdx = -1
			a.selectedInstIdx = -1
			for k := range a.selectedInstances {
				li, ii, ok := parseSelKey(k)
				if ok {
					a.setPrimarySelection(li, ii)
					break
				}
			}
		}
		return
	}
	a.selectedInstances[key] = true
	a.setPrimarySelection(layerIdx, instIdx)
}

func (a *App) findPathByID(id string) (VectorPath, bool) {
	for _, p := range a.doc.Paths {
		if p.ID == id {
			return p, true
		}
	}
	return VectorPath{}, false
}

func (a *App) findCircleByID(id string) (VectorCircle, bool) {
	for _, c := range a.doc.Circles {
		if c.ID == id {
			return c, true
		}
	}
	return VectorCircle{}, false
}

func dist(x1, y1, x2, y2 float64) float64 { return math.Hypot(x1-x2, y1-y2) }

func normalizeHexColor(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "")
	if strings.HasPrefix(s, "#") {
		if len(s) == 4 {
			return fmt.Sprintf("#%c%c%c%c%c%c", s[1], s[1], s[2], s[2], s[3], s[3])
		}
		if len(s) == 7 {
			return s
		}
	}
	if strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba(") {
		var r, g, b int
		if strings.HasPrefix(s, "rgba(") {
			if _, err := fmt.Sscanf(s, "rgba(%d,%d,%d", &r, &g, &b); err == nil {
				return fmt.Sprintf("#%02x%02x%02x", r, g, b)
			}
		}
		if _, err := fmt.Sscanf(s, "rgb(%d,%d,%d", &r, &g, &b); err == nil {
			return fmt.Sprintf("#%02x%02x%02x", r, g, b)
		}
	}
	return "#66e3ff"
}

func (a *App) applyTransformField(field string, value float64) {
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		kf, ok := a.getOrCreateInstanceKeyframe(li, ii, a.curFrame)
		if !ok {
			continue
		}
		switch field {
		case "x":
			kf.X = value
		case "y":
			kf.Y = value
		case "scaleX":
			kf.ScaleX = value
		case "scaleY":
			kf.ScaleY = value
		case "skewX":
			kf.SkewX = value
		case "skewY":
			kf.SkewY = value
		case "rotation":
			kf.Rotation = value
		case "anchorX":
			kf.AnchorX = value
		case "anchorY":
			kf.AnchorY = value
		}
		a.setInstanceKeyframe(li, ii, a.curFrame, kf)
	}
}

func (a *App) applyShapeColor(field, color string) {
	color = normalizeHexColor(color)
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.doc.Layers[li].Instances[ii]
		switch inst.ElementType {
		case "path":
			for pi := range a.doc.Paths {
				if a.doc.Paths[pi].ID != inst.ElementID {
					continue
				}
				if field == "fill" {
					a.doc.Paths[pi].Fill = color
				} else {
					a.doc.Paths[pi].Stroke = color
				}
				break
			}
		case "circle":
			for ci := range a.doc.Circles {
				if a.doc.Circles[ci].ID != inst.ElementID {
					continue
				}
				if field == "fill" {
					a.doc.Circles[ci].Fill = color
				} else {
					a.doc.Circles[ci].Stroke = color
				}
				break
			}
		}
	}
}

func (a *App) applyShapeNumeric(field string, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	if field == "strokeW" && value < 0 {
		value = 0
	}
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.doc.Layers[li].Instances[ii]
		switch inst.ElementType {
		case "path":
			for pi := range a.doc.Paths {
				if a.doc.Paths[pi].ID != inst.ElementID {
					continue
				}
				if field == "strokeW" {
					a.doc.Paths[pi].StrokeW = value
				}
				break
			}
		case "circle":
			for ci := range a.doc.Circles {
				if a.doc.Circles[ci].ID != inst.ElementID {
					continue
				}
				if field == "strokeW" {
					a.doc.Circles[ci].StrokeW = value
				}
				break
			}
		}
	}
}

func (a *App) applyRotationDelta(deltaRad float64) {
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		kf, ok := a.getOrCreateInstanceKeyframe(li, ii, a.curFrame)
		if !ok {
			continue
		}
		kf.Rotation += deltaRad
		a.setInstanceKeyframe(li, ii, a.curFrame, kf)
	}
}

func (a *App) updatePropertiesPanel() {
	hasSel := a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0
	controls := []js.Value{a.propPosX, a.propPosY, a.propScaleX, a.propScaleY, a.propSkewX, a.propSkewY, a.propRot, a.propRotDec, a.propRotInc, a.propAncX, a.propAncY, a.propFill, a.propStroke, a.propStrokeW}
	for _, c := range controls {
		if !c.Truthy() {
			continue
		}
		c.Set("disabled", !hasSel)
	}
	if !hasSel {
		return
	}
	kf, ok := a.getOrCreateInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame)
	if ok {
		a.propPosX.Set("value", fmt.Sprintf("%.2f", kf.X))
		a.propPosY.Set("value", fmt.Sprintf("%.2f", kf.Y))
		a.propScaleX.Set("value", fmt.Sprintf("%.3f", kf.ScaleX))
		a.propScaleY.Set("value", fmt.Sprintf("%.3f", kf.ScaleY))
		a.propSkewX.Set("value", fmt.Sprintf("%.3f", kf.SkewX))
		a.propSkewY.Set("value", fmt.Sprintf("%.3f", kf.SkewY))
		a.propRot.Set("value", fmt.Sprintf("%.3f", kf.Rotation))
		a.propAncX.Set("value", fmt.Sprintf("%.2f", kf.AnchorX))
		a.propAncY.Set("value", fmt.Sprintf("%.2f", kf.AnchorY))
	}
	inst := a.doc.Layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
	shape := inst.ElementType == "path" || inst.ElementType == "circle"
	a.propFill.Set("disabled", !shape)
	a.propStroke.Set("disabled", !shape)
	a.propStrokeW.Set("disabled", !shape)
	if inst.ElementType == "path" {
		if p, ok := a.findPathByID(inst.ElementID); ok {
			a.propFill.Set("value", normalizeHexColor(p.Fill))
			a.propStroke.Set("value", normalizeHexColor(p.Stroke))
			a.propStrokeW.Set("value", fmt.Sprintf("%.2f", p.StrokeW))
		}
	}
	if inst.ElementType == "circle" {
		if c, ok := a.findCircleByID(inst.ElementID); ok {
			a.propFill.Set("value", normalizeHexColor(c.Fill))
			a.propStroke.Set("value", normalizeHexColor(c.Stroke))
			a.propStrokeW.Set("value", fmt.Sprintf("%.2f", c.StrokeW))
		}
	}
}

func drawPathLocal(ctx js.Value, p VectorPath) {
	if len(p.Points) < 2 {
		return
	}
	ctx.Call("beginPath")
	ctx.Call("moveTo", p.Points[0].X, p.Points[0].Y)
	for i := 1; i < len(p.Points); i++ {
		prev := p.Points[i-1]
		cur := p.Points[i]
		ctx.Call("bezierCurveTo", prev.OutX, prev.OutY, cur.InX, cur.InY, cur.X, cur.Y)
	}
	if p.Closed {
		last := p.Points[len(p.Points)-1]
		first := p.Points[0]
		ctx.Call("bezierCurveTo", last.OutX, last.OutY, first.InX, first.InY, first.X, first.Y)
		ctx.Call("closePath")
		if p.Fill != "" && p.Fill != "transparent" {
			ctx.Set("fillStyle", p.Fill)
			ctx.Call("fill")
		}
	}
	ctx.Set("lineWidth", p.StrokeW)
	ctx.Set("strokeStyle", p.Stroke)
	ctx.Call("stroke")
}

func drawCircleLocal(ctx js.Value, c VectorCircle) {
	ctx.Set("fillStyle", c.Fill)
	ctx.Call("beginPath")
	ctx.Call("arc", c.CX, c.CY, c.Radius, 0, math.Pi*2)
	ctx.Call("fill")
	ctx.Set("lineWidth", c.StrokeW)
	ctx.Set("strokeStyle", c.Stroke)
	ctx.Call("stroke")
}

func pathLocalBounds(p VectorPath) (float64, float64, float64, float64, bool) {
	if len(p.Points) == 0 {
		return 0, 0, 0, 0, false
	}
	minX, minY := p.Points[0].X, p.Points[0].Y
	maxX, maxY := minX, minY
	update := func(x, y float64) {
		if x < minX {
			minX = x
		}
		if y < minY {
			minY = y
		}
		if x > maxX {
			maxX = x
		}
		if y > maxY {
			maxY = y
		}
	}
	for _, pt := range p.Points {
		update(pt.X, pt.Y)
		update(pt.InX, pt.InY)
		update(pt.OutX, pt.OutY)
	}
	return minX, minY, maxX, maxY, true
}

func (a *App) instanceBoundsWorld(layerIdx, instIdx int) (float64, float64, float64, float64, bool) {
	inst := a.doc.Layers[layerIdx].Instances[instIdx]
	kf, ok := a.getInstanceKeyframe(layerIdx, instIdx, a.curFrame)
	if !ok {
		return 0, 0, 0, 0, false
	}
	m := instanceMatrix(kf)
	setBounds := func(pts [][2]float64) (float64, float64, float64, float64, bool) {
		if len(pts) == 0 {
			return 0, 0, 0, 0, false
		}
		wx, wy := matApply(m, pts[0][0], pts[0][1])
		minX, minY, maxX, maxY := wx, wy, wx, wy
		for i := 1; i < len(pts); i++ {
			x, y := matApply(m, pts[i][0], pts[i][1])
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
		return minX, minY, maxX, maxY, true
	}

	if inst.ElementType == "path" {
		p, ok := a.findPathByID(inst.ElementID)
		if !ok {
			return 0, 0, 0, 0, false
		}
		minX, minY, maxX, maxY, ok := pathLocalBounds(p)
		if !ok {
			return 0, 0, 0, 0, false
		}
		return setBounds([][2]float64{{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY}})
	}
	if inst.ElementType == "circle" {
		c, ok := a.findCircleByID(inst.ElementID)
		if !ok {
			return 0, 0, 0, 0, false
		}
		return setBounds([][2]float64{
			{c.CX - c.Radius, c.CY - c.Radius},
			{c.CX + c.Radius, c.CY - c.Radius},
			{c.CX + c.Radius, c.CY + c.Radius},
			{c.CX - c.Radius, c.CY + c.Radius},
		})
	}
	return 0, 0, 0, 0, false
}

func (a *App) pickInstanceAt(x, y float64) (int, int, bool) {
	for li := len(a.doc.Layers) - 1; li >= 0; li-- {
		layer := a.doc.Layers[li]
		for ii := len(layer.Instances) - 1; ii >= 0; ii-- {
			inst := layer.Instances[ii]
			if inst.ElementType != "path" && inst.ElementType != "circle" {
				continue
			}
			minX, minY, maxX, maxY, ok := a.instanceBoundsWorld(li, ii)
			if !ok {
				continue
			}
			if x >= minX && x <= maxX && y >= minY && y <= maxY {
				return li, ii, true
			}
		}
	}
	return -1, -1, false
}

func (a *App) selectedAnchorWorld() (float64, float64, bool) {
	if a.selectedLayerIdx < 0 || a.selectedInstIdx < 0 {
		return 0, 0, false
	}
	kf, ok := a.getInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame)
	if !ok {
		return 0, 0, false
	}
	return kf.X + kf.AnchorX, kf.Y + kf.AnchorY, true
}

func movePointHandleWeighted(pt BezierPoint, handle string, x, y float64) BezierPoint {
	switch handle {
	case "in":
		// Move incoming handle directly.
		pt.InX = x
		pt.InY = y
		// Keep opposite tangent colinear for smooth weighted tangent behavior.
		vx := pt.X - x
		vy := pt.Y - y
		vlen := math.Hypot(vx, vy)
		ovx := pt.OutX - pt.X
		ovy := pt.OutY - pt.Y
		olen := math.Hypot(ovx, ovy)
		if olen < 1e-4 {
			olen = vlen
		}
		if vlen > 1e-4 {
			nx := vx / vlen
			ny := vy / vlen
			pt.OutX = pt.X + nx*olen
			pt.OutY = pt.Y + ny*olen
		}
	case "out":
		// Move outgoing handle directly.
		pt.OutX = x
		pt.OutY = y
		// Keep opposite tangent colinear for smooth weighted tangent behavior.
		vx := pt.X - x
		vy := pt.Y - y
		vlen := math.Hypot(vx, vy)
		ivx := pt.InX - pt.X
		ivy := pt.InY - pt.Y
		ilen := math.Hypot(ivx, ivy)
		if ilen < 1e-4 {
			ilen = vlen
		}
		if vlen > 1e-4 {
			nx := vx / vlen
			ny := vy / vlen
			pt.InX = pt.X + nx*ilen
			pt.InY = pt.Y + ny*ilen
		}
	}
	return pt
}

func (a *App) clearPenDraft() {
	a.penEditing = false
	a.penPoints = nil
	a.penMouseDown = false
	a.penDragIndex = -1
	a.penDragMoved = false
}

func (a *App) commitPenPath(closed bool) {
	if len(a.penPoints) < 2 {
		a.clearPenDraft()
		return
	}

	sumX := 0.0
	sumY := 0.0
	for _, p := range a.penPoints {
		sumX += p.X
		sumY += p.Y
	}
	cx := sumX / float64(len(a.penPoints))
	cy := sumY / float64(len(a.penPoints))

	localPts := make([]BezierPoint, 0, len(a.penPoints))
	for _, p := range a.penPoints {
		localPts = append(localPts, BezierPoint{
			X:    p.X - cx,
			Y:    p.Y - cy,
			InX:  p.InX - cx,
			InY:  p.InY - cy,
			OutX: p.OutX - cx,
			OutY: p.OutY - cy,
		})
	}

	pathID := a.nextPathID()
	path := VectorPath{
		ID:      pathID,
		Points:  localPts,
		Stroke:  "#66e3ff",
		StrokeW: 2,
		Closed:  closed,
	}
	if closed {
		path.Fill = "rgba(102, 227, 255, 0.25)"
	} else {
		path.Fill = "transparent"
	}

	a.doc.Paths = append(a.doc.Paths, path)
	kf := defaultKeyframeAt(a.curFrame)
	kf.X = cx
	kf.Y = cy
	kf.AnchorX = 0
	kf.AnchorY = 0
	a.addPathInstanceToSelectedLayers(pathID, kf)
	if closed {
		a.statusEl.Set("textContent", "Closed path created on selected layers")
	} else {
		a.statusEl.Set("textContent", "Open stroked path created on selected layers")
	}
	a.clearPenDraft()
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
	// left toolbar icon tools
	iconTools := d.Call("querySelectorAll", ".iconbtn")
	for i := 0; i < iconTools.Length(); i++ {
		b := iconTools.Index(i)
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			tool := this.Get("dataset").Get("tool").String()
			if tool != "" {
				a.setActiveTool(tool)
			}
			return nil
		})
		b.Call("addEventListener", "click", cb)
	}
	// properties panel bindings
	readNumber := func(this js.Value) (float64, bool) {
		v := this.Get("valueAsNumber").Float()
		if !math.IsNaN(v) && !math.IsInf(v, 0) {
			return v, true
		}
		s := strings.TrimSpace(this.Get("value").String())
		if s == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return 0, false
		}
		return parsed, true
	}
	bindNum := func(el js.Value, field string) {
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			v, ok := readNumber(this)
			if !ok {
				return nil
			}
			a.applyTransformField(field, v)
			return nil
		})
		a.holdCallback(cb)
		el.Call("addEventListener", "input", cb)
		el.Call("addEventListener", "change", cb)
	}
	bindColor := func(el js.Value, field string) {
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			a.applyShapeColor(field, this.Get("value").String())
			return nil
		})
		a.holdCallback(cb)
		el.Call("addEventListener", "input", cb)
		el.Call("addEventListener", "change", cb)
	}
	bindShapeNum := func(el js.Value, field string) {
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			v, ok := readNumber(this)
			if !ok {
				return nil
			}
			a.applyShapeNumeric(field, v)
			return nil
		})
		a.holdCallback(cb)
		el.Call("addEventListener", "input", cb)
		el.Call("addEventListener", "change", cb)
	}
	bindNum(a.propPosX, "x")
	bindNum(a.propPosY, "y")
	bindNum(a.propScaleX, "scaleX")
	bindNum(a.propScaleY, "scaleY")
	bindNum(a.propSkewX, "skewX")
	bindNum(a.propSkewY, "skewY")
	bindNum(a.propRot, "rotation")
	bindNum(a.propAncX, "anchorX")
	bindNum(a.propAncY, "anchorY")
	bindColor(a.propFill, "fill")
	bindColor(a.propStroke, "stroke")
	bindShapeNum(a.propStrokeW, "strokeW")
	rotDecCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyRotationDelta(-5 * math.Pi / 180)
		return nil
	})
	rotIncCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyRotationDelta(5 * math.Pi / 180)
		return nil
	})
	a.holdCallback(rotDecCb)
	a.holdCallback(rotIncCb)
	a.propRotDec.Call("addEventListener", "click", rotDecCb)
	a.propRotInc.Call("addEventListener", "click", rotIncCb)

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
			for i := range a.doc.Layers {
				a.doc.Layers[i].Selected = false
			}
			n := len(a.doc.Layers) + 1
			a.doc.Layers = append([]Layer{{
				Name:        fmt.Sprintf("Layer %d", n),
				Description: fmt.Sprintf("User created layer %d", n),
				Color:       "#c77dff",
				Selected:    true,
				Instances: []ElementInstance{{
					ID:          fmt.Sprintf("layer-%d-instance-1", n),
					Name:        "Symbol Instance",
					Description: "Default element instance",
					Keyframes:   make(map[int]InstanceKeyframe),
				}},
			}}, a.doc.Layers...)
			a.updateSelectedLayerLabel()
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
		if key == "Enter" && a.activeTool == "pencil" && len(a.penPoints) >= 2 {
			e.Call("preventDefault")
			a.commitPenPath(false)
		}
		if key == "Escape" && a.activeTool == "pencil" && a.penEditing {
			e.Call("preventDefault")
			a.clearPenDraft()
			a.statusEl.Set("textContent", "Pen path canceled")
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

		// click layer header area to select layer (Ctrl/Cmd toggles)
		if x <= a.headerW {
			rowTop := 14.0
			if y >= rowTop {
				layerIdx := int((y - rowTop) / a.layerH)
				if layerIdx >= 0 && layerIdx < len(a.doc.Layers) {
					additive := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()
					a.selectLayer(layerIdx, additive)
					return nil
				}
			}
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
		a.dragMode = ""
		if a.drawingCircle {
			a.drawingCircle = false
		}
		if a.penMouseDown {
			a.penMouseDown = false
			a.penDragIndex = -1
			a.penDragMoved = false
		}
		return nil
	}))

	// stage drawing interactions
	a.stageCanvas.Call("addEventListener", "mousedown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()

		switch a.activeTool {
		case "select":
			a.lastMouseX = x
			a.lastMouseY = y
			a.dragMode = ""
			a.marqueeActive = false
			additive := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool() || e.Get("shiftKey").Bool()
			if a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0 {
				ax, ay, ok := a.selectedAnchorWorld()
				if ok && dist(x, y, ax, ay) <= 8 {
					a.dragMode = "anchor"
					return nil
				}
				minX, minY, maxX, maxY, ok := a.instanceBoundsWorld(a.selectedLayerIdx, a.selectedInstIdx)
				if ok {
					rotateX := (minX + maxX) / 2
					rotateY := minY - 18
					scaleX, scaleY := maxX, maxY
					skewXx, skewXy := maxX+14, (minY+maxY)/2
					skewYx, skewYy := (minX+maxX)/2, maxY+14
					if dist(x, y, rotateX, rotateY) <= 8 {
						a.dragMode = "rotate"
						return nil
					}
					if dist(x, y, scaleX, scaleY) <= 8 {
						a.dragMode = "scale"
						return nil
					}
					if dist(x, y, skewXx, skewXy) <= 8 {
						a.dragMode = "skewX"
						return nil
					}
					if dist(x, y, skewYx, skewYy) <= 8 {
						a.dragMode = "skewY"
						return nil
					}
				}
			}
			if li, ii, ok := a.pickInstanceAt(x, y); ok {
				if additive {
					a.toggleInstanceSelection(li, ii)
					if !a.isInstanceSelected(li, ii) {
						a.dragMode = ""
						a.updateSelectedLayerLabel()
						return nil
					}
				} else {
					a.setSingleInstanceSelection(li, ii)
				}
				a.selectedPathPt = -1
				a.selectedHandle = ""
				if !additive {
					for i := range a.doc.Layers {
						a.doc.Layers[i].Selected = i == li
					}
				}
				a.updateSelectedLayerLabel()
				a.dragMode = "move"
			} else {
				if !additive {
					a.clearInstanceSelection()
				}
				a.marqueeActive = true
				a.marqueeStartX = x
				a.marqueeStartY = y
				a.marqueeNowX = x
				a.marqueeNowY = y
				a.marqueeAdditive = additive
			}
		case "subselect":
			a.lastMouseX = x
			a.lastMouseY = y
			a.dragMode = ""
			if a.selectedLayerIdx < 0 || a.selectedInstIdx < 0 {
				if li, ii, ok := a.pickInstanceAt(x, y); ok {
					a.setSingleInstanceSelection(li, ii)
				} else {
					return nil
				}
			}
			inst := a.doc.Layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
			if inst.ElementType != "path" {
				return nil
			}
			p, ok := a.findPathByID(inst.ElementID)
			if !ok {
				return nil
			}
			kf, ok := a.getInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame)
			if !ok {
				return nil
			}
			m := instanceMatrix(kf)
			closest := -1
			closestHandle := ""
			best := 1e9
			for i, pt := range p.Points {
				ax, ay := matApply(m, pt.X, pt.Y)
				d := dist(x, y, ax, ay)
				if d < best && d <= 8 {
					best = d
					closest = i
					closestHandle = "anchor"
				}
				hx, hy := matApply(m, pt.InX, pt.InY)
				d = dist(x, y, hx, hy)
				if d < best && d <= 7 {
					best = d
					closest = i
					closestHandle = "in"
				}
				hx, hy = matApply(m, pt.OutX, pt.OutY)
				d = dist(x, y, hx, hy)
				if d < best && d <= 7 {
					best = d
					closest = i
					closestHandle = "out"
				}
			}
			a.selectedPathPt = closest
			a.selectedHandle = closestHandle
			if closest >= 0 {
				a.dragMode = "subselect"
			}
		case "circle":
			a.circleStartX = x
			a.circleStartY = y
			a.circleNowX = x
			a.circleNowY = y
			a.drawingCircle = true
		case "pencil":
			a.penMouseX = x
			a.penMouseY = y
			if len(a.penPoints) >= 2 {
				first := a.penPoints[0]
				if math.Hypot(x-first.X, y-first.Y) <= 8 {
					a.commitPenPath(true)
					return nil
				}
			}
			a.penEditing = true
			a.penMouseDown = true
			a.penDragMoved = false
			a.penPoints = append(a.penPoints, BezierPoint{
				X:    x,
				Y:    y,
				InX:  x,
				InY:  y,
				OutX: x,
				OutY: y,
			})
			a.penDragIndex = len(a.penPoints) - 1
		}
		return nil
	}))
	a.stageCanvas.Call("addEventListener", "mousemove", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()

		if a.marqueeActive {
			a.marqueeNowX = x
			a.marqueeNowY = y
		}

		if a.dragMode != "" && len(a.selectedInstancePairs()) > 0 {
			dx := x - a.lastMouseX
			dy := y - a.lastMouseY
			for _, pair := range a.selectedInstancePairs() {
				li, ii := pair[0], pair[1]
				if kf, ok := a.getOrCreateInstanceKeyframe(li, ii, a.curFrame); ok {
					switch a.dragMode {
					case "move":
						kf.X += dx
						kf.Y += dy
						a.setInstanceKeyframe(li, ii, a.curFrame, kf)
					case "anchor":
						kf.AnchorX += dx
						kf.AnchorY += dy
						a.setInstanceKeyframe(li, ii, a.curFrame, kf)
					case "rotate":
						ax, ay := kf.X+kf.AnchorX, kf.Y+kf.AnchorY
						prevA := math.Atan2(a.lastMouseY-ay, a.lastMouseX-ax)
						curA := math.Atan2(y-ay, x-ax)
						kf.Rotation += curA - prevA
						a.setInstanceKeyframe(li, ii, a.curFrame, kf)
					case "scale":
						ax, ay := kf.X+kf.AnchorX, kf.Y+kf.AnchorY
						prevD := math.Hypot(a.lastMouseX-ax, a.lastMouseY-ay)
						curD := math.Hypot(x-ax, y-ay)
						if prevD > 1e-3 {
							s := curD / prevD
							kf.ScaleX *= s
							kf.ScaleY *= s
							a.setInstanceKeyframe(li, ii, a.curFrame, kf)
						}
					case "skewX":
						kf.SkewX += dx * 0.01
						a.setInstanceKeyframe(li, ii, a.curFrame, kf)
					case "skewY":
						kf.SkewY += dy * 0.01
						a.setInstanceKeyframe(li, ii, a.curFrame, kf)
					}
				}
			}
		}

		if a.dragMode == "subselect" && a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0 && a.selectedPathPt >= 0 {
			inst := a.doc.Layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
			if inst.ElementType == "path" {
				for pi := range a.doc.Paths {
					if a.doc.Paths[pi].ID != inst.ElementID {
						continue
					}
					kf, ok := a.getInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame)
					if !ok {
						break
					}
					inv, ok := matInvert(instanceMatrix(kf))
					if !ok {
						break
					}
					lx, ly := matApply(inv, x, y)
					pt := a.doc.Paths[pi].Points[a.selectedPathPt]
					switch a.selectedHandle {
					case "anchor":
						dpx := lx - pt.X
						dpy := ly - pt.Y
						pt.X = lx
						pt.Y = ly
						pt.InX += dpx
						pt.InY += dpy
						pt.OutX += dpx
						pt.OutY += dpy
					case "in":
						pt = movePointHandleWeighted(pt, "in", lx, ly)
					case "out":
						pt = movePointHandleWeighted(pt, "out", lx, ly)
					}
					a.doc.Paths[pi].Points[a.selectedPathPt] = pt
					break
				}
			}
		}
		if a.drawingCircle {
			a.circleNowX = x
			a.circleNowY = y
		}
		if a.activeTool == "pencil" {
			a.penMouseX = x
			a.penMouseY = y
		}
		if a.penMouseDown && a.penDragIndex >= 0 && a.penDragIndex < len(a.penPoints) {
			p := a.penPoints[a.penDragIndex]
			if math.Hypot(x-p.X, y-p.Y) >= 2 {
				a.penDragMoved = true
			}
			if a.penDragMoved {
				p.OutX = x
				p.OutY = y
				p.InX = 2*p.X - x
				p.InY = 2*p.Y - y
				a.penPoints[a.penDragIndex] = p
			}
		}
		a.lastMouseX = x
		a.lastMouseY = y
		return nil
	}))
	a.stageCanvas.Call("addEventListener", "mouseup", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		a.dragMode = ""
		if a.marqueeActive {
			a.marqueeNowX = x
			a.marqueeNowY = y
			minX := math.Min(a.marqueeStartX, a.marqueeNowX)
			maxX := math.Max(a.marqueeStartX, a.marqueeNowX)
			minY := math.Min(a.marqueeStartY, a.marqueeNowY)
			maxY := math.Max(a.marqueeStartY, a.marqueeNowY)
			if !a.marqueeAdditive {
				a.selectedInstances = make(map[string]bool)
				a.selectedLayerIdx = -1
				a.selectedInstIdx = -1
			}
			for li := range a.doc.Layers {
				for ii := range a.doc.Layers[li].Instances {
					inst := a.doc.Layers[li].Instances[ii]
					if inst.ElementType != "path" && inst.ElementType != "circle" {
						continue
					}
					bx0, by0, bx1, by1, ok := a.instanceBoundsWorld(li, ii)
					if !ok {
						continue
					}
					intersects := bx1 >= minX && bx0 <= maxX && by1 >= minY && by0 <= maxY
					if intersects {
						a.selectedInstances[selKey(li, ii)] = true
						a.selectedLayerIdx = li
						a.selectedInstIdx = ii
					}
				}
			}
			a.marqueeActive = false
			a.updateSelectedLayerLabel()
		}
		if a.drawingCircle {
			a.circleNowX = x
			a.circleNowY = y
			r := math.Hypot(a.circleNowX-a.circleStartX, a.circleNowY-a.circleStartY)
			if r >= 2 {
				circleID := a.nextCircleID()
				a.doc.Circles = append(a.doc.Circles, VectorCircle{
					ID:      circleID,
					CX:      0,
					CY:      0,
					Radius:  r,
					Fill:    "rgba(255, 204, 102, 0.35)",
					Stroke:  "#ffcc66",
					StrokeW: 2,
				})
				kf := defaultKeyframeAt(a.curFrame)
				kf.X = a.circleStartX
				kf.Y = a.circleStartY
				kf.AnchorX = 0
				kf.AnchorY = 0
				a.addCircleInstanceToSelectedLayers(circleID, kf)
				a.statusEl.Set("textContent", "Circle created")
			}
			a.drawingCircle = false
		}
		if a.penMouseDown {
			a.penMouseDown = false
			a.penDragIndex = -1
		}
		a.lastMouseX = x
		a.lastMouseY = y
		return nil
	}))

	a.setActiveTool(a.activeTool)
}

func (a *App) setActiveTool(tool string) {
	d := js.Global().Get("document")
	if a.activeTool == "pencil" && tool != "pencil" && a.penEditing {
		a.clearPenDraft()
	}
	a.activeTool = tool

	btns := d.Call("querySelectorAll", ".toolbtn")
	for i := 0; i < btns.Length(); i++ {
		b := btns.Index(i)
		if b.Get("dataset").Get("tool").String() == tool {
			b.Get("classList").Call("add", "active")
		} else {
			b.Get("classList").Call("remove", "active")
		}
	}
	iconBtns := d.Call("querySelectorAll", ".iconbtn")
	for i := 0; i < iconBtns.Length(); i++ {
		b := iconBtns.Index(i)
		if b.Get("dataset").Get("tool").String() == tool {
			b.Get("classList").Call("add", "active")
		} else {
			b.Get("classList").Call("remove", "active")
		}
	}
	// friendly name
	name := map[string]string{
		"select":    "Selection",
		"subselect": "Subselection",
		"transform": "Transform",
		"text":      "Text",
		"shape":     "Shape",
		"pencil":    "Pencil",
		"circle":    "Circle",
		"line":      "Line",
		"tween":     "Classic Tween",
		"action":    "Action Script",
	}[tool]
	if name == "" {
		name = tool
	}
	a.selToolEl.Set("textContent", name)
	if tool == "circle" || tool == "pencil" {
		a.stageCanvas.Get("style").Set("cursor", "crosshair")
	} else {
		a.stageCanvas.Get("style").Set("cursor", "default")
	}
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
	a.updatePropertiesPanel()

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

	// draw shape instances
	for li := range a.doc.Layers {
		layer := a.doc.Layers[li]
		for ii := range layer.Instances {
			inst := layer.Instances[ii]
			kf, ok := a.getInstanceKeyframe(li, ii, a.curFrame)
			if !ok {
				continue
			}
			if inst.ElementType != "path" && inst.ElementType != "circle" {
				continue
			}
			ctx.Call("save")
			m := instanceMatrix(kf)
			ctx.Call("transform", m.a, m.b, m.c, m.d, m.e, m.f)
			if inst.ElementType == "path" {
				if p, ok := a.findPathByID(inst.ElementID); ok {
					drawPathLocal(ctx, p)
				}
			}
			if inst.ElementType == "circle" {
				if c, ok := a.findCircleByID(inst.ElementID); ok {
					drawCircleLocal(ctx, c)
				}
			}
			ctx.Call("restore")
		}
	}

	// in-progress circle preview
	if a.drawingCircle {
		r := math.Hypot(a.circleNowX-a.circleStartX, a.circleNowY-a.circleStartY)
		ctx.Set("fillStyle", "rgba(255, 255, 255, 0.18)")
		ctx.Call("beginPath")
		ctx.Call("arc", a.circleStartX, a.circleStartY, r, 0, math.Pi*2)
		ctx.Call("fill")
		ctx.Set("lineWidth", 1.5)
		ctx.Set("strokeStyle", "rgba(255, 255, 255, 0.9)")
		ctx.Call("stroke")
	}
	if a.penEditing && len(a.penPoints) >= 1 {
		ctx.Call("beginPath")
		ctx.Call("moveTo", a.penPoints[0].X, a.penPoints[0].Y)
		for i := 1; i < len(a.penPoints); i++ {
			prev := a.penPoints[i-1]
			cur := a.penPoints[i]
			ctx.Call("bezierCurveTo", prev.OutX, prev.OutY, cur.InX, cur.InY, cur.X, cur.Y)
		}
		if !a.penMouseDown && len(a.penPoints) > 0 {
			last := a.penPoints[len(a.penPoints)-1]
			ctx.Call("lineTo", a.penMouseX, a.penMouseY)
			ctx.Set("strokeStyle", "rgba(255,255,255,0.35)")
			ctx.Set("lineWidth", 1)
			ctx.Call("stroke")
			ctx.Call("beginPath")
			ctx.Call("arc", last.X, last.Y, 2.5, 0, math.Pi*2)
			ctx.Set("fillStyle", "rgba(255,255,255,0.9)")
			ctx.Call("fill")
		}
		ctx.Set("lineWidth", 2)
		ctx.Set("strokeStyle", "rgba(255,255,255,0.9)")
		ctx.Call("stroke")

		// anchor and handle preview
		for i := range a.penPoints {
			p := a.penPoints[i]
			ctx.Set("strokeStyle", "rgba(255,255,255,0.45)")
			ctx.Set("lineWidth", 1)
			ctx.Call("beginPath")
			ctx.Call("moveTo", p.X, p.Y)
			ctx.Call("lineTo", p.InX, p.InY)
			ctx.Call("moveTo", p.X, p.Y)
			ctx.Call("lineTo", p.OutX, p.OutY)
			ctx.Call("stroke")

			ctx.Set("fillStyle", "#ffffff")
			ctx.Call("beginPath")
			ctx.Call("arc", p.X, p.Y, 3, 0, math.Pi*2)
			ctx.Call("fill")
			ctx.Call("beginPath")
			ctx.Call("arc", p.InX, p.InY, 2, 0, math.Pi*2)
			ctx.Call("fill")
			ctx.Call("beginPath")
			ctx.Call("arc", p.OutX, p.OutY, 2, 0, math.Pi*2)
			ctx.Call("fill")
		}

		// highlight start anchor to close path
		if len(a.penPoints) >= 2 {
			first := a.penPoints[0]
			ctx.Set("strokeStyle", "rgba(102,227,255,0.9)")
			ctx.Set("lineWidth", 1.5)
			ctx.Call("beginPath")
			ctx.Call("arc", first.X, first.Y, 6, 0, math.Pi*2)
			ctx.Call("stroke")
		}
	}

	// selected instance transform controls
	for _, pair := range a.selectedInstancePairs() {
		li, ii := pair[0], pair[1]
		minX, minY, maxX, maxY, ok := a.instanceBoundsWorld(li, ii)
		if !ok {
			continue
		}
		ctx.Set("strokeStyle", "rgba(255, 204, 102, 0.95)")
		ctx.Set("lineWidth", 1.5)
		ctx.Call("strokeRect", minX, minY, maxX-minX, maxY-minY)

		rotateX := (minX + maxX) / 2
		rotateY := minY - 18
		scaleX, scaleY := maxX, maxY
		skewXx, skewXy := maxX+14, (minY+maxY)/2
		skewYx, skewYy := (minX+maxX)/2, maxY+14
		kf, ok := a.getInstanceKeyframe(li, ii, a.curFrame)
		if !ok {
			continue
		}
		ax, ay := kf.X+kf.AnchorX, kf.Y+kf.AnchorY
		ctx.Set("fillStyle", "#ffcc66")
		for _, h := range [][2]float64{{rotateX, rotateY}, {scaleX, scaleY}, {skewXx, skewXy}, {skewYx, skewYy}, {ax, ay}} {
			ctx.Call("beginPath")
			ctx.Call("arc", h[0], h[1], 5, 0, math.Pi*2)
			ctx.Call("fill")
		}
	}

	// marquee selection rectangle
	if a.marqueeActive {
		minX := math.Min(a.marqueeStartX, a.marqueeNowX)
		maxX := math.Max(a.marqueeStartX, a.marqueeNowX)
		minY := math.Min(a.marqueeStartY, a.marqueeNowY)
		maxY := math.Max(a.marqueeStartY, a.marqueeNowY)
		ctx.Set("fillStyle", "rgba(255,204,102,0.12)")
		ctx.Call("fillRect", minX, minY, maxX-minX, maxY-minY)
		ctx.Set("strokeStyle", "rgba(255,204,102,0.95)")
		ctx.Set("lineWidth", 1)
		ctx.Call("strokeRect", minX, minY, maxX-minX, maxY-minY)
	}

	// subselect display: all anchors + selected point handles
	if a.activeTool == "subselect" && a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0 {
		inst := a.doc.Layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
		if inst.ElementType == "path" {
			if p, ok := a.findPathByID(inst.ElementID); ok {
				if kf, ok := a.getInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame); ok {
					m := instanceMatrix(kf)

					// Show all path anchors so users can subselect specific points.
					for i, pt := range p.Points {
						ax, ay := matApply(m, pt.X, pt.Y)
						if i == a.selectedPathPt {
							ctx.Set("fillStyle", "#66e3ff")
						} else {
							ctx.Set("fillStyle", "#ffffff")
						}
						ctx.Call("beginPath")
						ctx.Call("arc", ax, ay, 4, 0, math.Pi*2)
						ctx.Call("fill")
					}

					// Reveal and allow editing handles for selected point.
					if a.selectedPathPt >= 0 && a.selectedPathPt < len(p.Points) {
						pt := p.Points[a.selectedPathPt]
						ax, ay := matApply(m, pt.X, pt.Y)
						inx, iny := matApply(m, pt.InX, pt.InY)
						outx, outy := matApply(m, pt.OutX, pt.OutY)
						ctx.Set("strokeStyle", "rgba(255,255,255,0.8)")
						ctx.Set("lineWidth", 1)
						ctx.Call("beginPath")
						ctx.Call("moveTo", ax, ay)
						ctx.Call("lineTo", inx, iny)
						ctx.Call("moveTo", ax, ay)
						ctx.Call("lineTo", outx, outy)
						ctx.Call("stroke")

						ctx.Set("fillStyle", "#9fd6ff")
						ctx.Call("beginPath")
						ctx.Call("arc", inx, iny, 3.5, 0, math.Pi*2)
						ctx.Call("fill")
						ctx.Call("beginPath")
						ctx.Call("arc", outx, outy, 3.5, 0, math.Pi*2)
						ctx.Call("fill")
					}
				}
			}
		}
	}

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
		if layer.Selected {
			ctx.Set("fillStyle", "rgba(255, 204, 102, 0.12)")
		} else if i%2 == 0 {
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
		for i := range a.doc.Layers {
			a.doc.Layers[i].Selected = false
		}
		n := len(a.doc.Layers) + 1
		a.doc.Layers = append([]Layer{{
			Name:        fmt.Sprintf("Layer %d", n),
			Description: fmt.Sprintf("User created layer %d", n),
			Color:       "#c77dff",
			Selected:    true,
			Instances: []ElementInstance{{
				ID:          fmt.Sprintf("layer-%d-instance-1", n),
				Name:        "Symbol Instance",
				Description: "Default element instance",
				Keyframes:   make(map[int]InstanceKeyframe),
			}},
		}}, a.doc.Layers...)
		a.updateSelectedLayerLabel()
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
