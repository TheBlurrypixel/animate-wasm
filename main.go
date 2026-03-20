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

type Symbol struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	SymbolType string            `json:"symbolType"`
	AssetURL   string            `json:"assetURL,omitempty"`
	BitmapData string            `json:"bitmapData,omitempty"`
	BitmapW    float64           `json:"bitmapW,omitempty"`
	BitmapH    float64           `json:"bitmapH,omitempty"`
	Layers     []Layer           `json:"layers,omitempty"`
	Instances  []ElementInstance `json:"instances"`
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
	EaseMode string  `json:"easeMode,omitempty"`
	EaseDir  string  `json:"easeDir,omitempty"`
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
	Background  string         `json:"background"`
	TotalFrames int            `json:"totalFrames"`
	Layers      []Layer        `json:"layers"`
	Symbols     []Symbol       `json:"symbols"`
	Circles     []VectorCircle `json:"circles"`
	Paths       []VectorPath   `json:"paths"`
}

type appSnapshot struct {
	Doc                     Document
	CurFrame                int
	SelectedLayerIdx        int
	SelectedInstIdx         int
	SelectedInstances       map[string]bool
	SelectedTweenLayerIdx   int
	SelectedTweenInstIdx    int
	SelectedTweenStartFrame int
	SelectedTweenEndFrame   int
	SelectedLibrarySymbolID string
}

func newDefaultDocument() Document {
	doc := Document{
		Name:        "scene-1",
		Width:       640,
		Height:      360,
		FPS:         24,
		Background:  "#808080",
		TotalFrames: 120,
		Layers: []Layer{
			{
				Name:        "Layer 1",
				Description: "Default empty layer",
				Color:       "#c77dff",
				Selected:    true,
				Instances:   []ElementInstance{},
			},
		},
		Symbols: []Symbol{},
		Circles: []VectorCircle{},
		Paths:   []VectorPath{},
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
		EaseMode: "linear",
		EaseDir:  "out",
	}
}

func normalizeEaseMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "linear":
		return "linear"
	case "sine":
		return "sine"
	case "cubic":
		return "cubic"
	case "quint", "quintic", "quantic":
		return "quintic"
	default:
		return "linear"
	}
}

func normalizeEaseDir(dir string) string {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "in":
		return "in"
	case "", "out":
		return "out"
	default:
		return "out"
	}
}

func easeValue(t float64, mode, dir string) float64 {
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 1
	}
	mode = normalizeEaseMode(mode)
	dir = normalizeEaseDir(dir)
	if mode == "linear" {
		return t
	}
	if dir == "in" {
		switch mode {
		case "sine":
			return 1 - math.Cos((t*math.Pi)/2)
		case "cubic":
			return t * t * t
		case "quintic":
			return t * t * t * t * t
		}
	}
	switch mode {
	case "sine":
		return math.Sin((t * math.Pi) / 2)
	case "cubic":
		u := 1 - t
		return 1 - u*u*u
	case "quintic":
		u := 1 - t
		return 1 - u*u*u*u*u
	default:
		return t
	}
}

func lerpFloat(a, b, t float64) float64 {
	return a + (b-a)*t
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
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return
	}
	if instanceIdx < 0 || instanceIdx >= len((*layers)[layerIdx].Instances) {
		return
	}
	inst := &(*layers)[layerIdx].Instances[instanceIdx]
	kf, ok := a.getInstanceKeyframe(layerIdx, instanceIdx, frame)
	if !ok {
		kf = defaultKeyframeAt(frame)
	}
	kf.Frame = frame
	inst.Keyframes[frame] = kf
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

	stageCanvas   js.Value
	stageCtx      js.Value
	stageViewport js.Value

	tlCanvas js.Value
	tlCtx    js.Value

	statusEl               js.Value
	sceneNameEl            js.Value
	stageTimelineEl        js.Value
	docSizeEl              js.Value
	docFpsEl               js.Value
	propDocName            js.Value
	propDocWidth           js.Value
	propDocHeight          js.Value
	propDocFps             js.Value
	propDocBg              js.Value
	curFrameEl             js.Value
	isPlayEl               js.Value
	selNameEl              js.Value
	selToolEl              js.Value
	libraryListEl          js.Value
	propertiesPanelEl      js.Value
	libraryPanelEl         js.Value
	propName               js.Value
	propPosX               js.Value
	propPosY               js.Value
	propScaleX             js.Value
	propScaleY             js.Value
	propScaleLock          js.Value
	propSkewX              js.Value
	propSkewY              js.Value
	propRot                js.Value
	propRotDec             js.Value
	propRotInc             js.Value
	propAncX               js.Value
	propAncY               js.Value
	propFill               js.Value
	propStroke             js.Value
	propStrokeW            js.Value
	propEaseMode           js.Value
	propEaseDir            js.Value
	layerCtxMenu           js.Value
	stageCtxMenu           js.Value
	keyframeCtxMenu        js.Value
	autoKeyBtn             js.Value
	docDialog              js.Value
	docDlgName             js.Value
	docDlgWidth            js.Value
	docDlgHeight           js.Value
	docDlgFps              js.Value
	docDlgBg               js.Value
	docDlgCancel           js.Value
	docDlgSave             js.Value
	settingsDialog         js.Value
	settingsMaxUndo        js.Value
	settingsDlgCancel      js.Value
	settingsDlgSave        js.Value
	shapeToolBtn           js.Value
	shapeToolIconEl        js.Value
	shapeToolCorner        js.Value
	shapeToolMenu          js.Value
	toolFill               js.Value
	toolStroke             js.Value
	colorPickerPopover     js.Value
	colorPickerPreview     js.Value
	colorPickerPreviewText js.Value
	colorPickerColor       js.Value
	colorPickerAlpha       js.Value
	colorPickerAlphaValue  js.Value
	bitmapImages           map[string]js.Value
	timelinePath           []string

	// timeline state
	curFrame       int // 1-based
	playing        bool
	autoKey        bool
	lockScale      bool
	maxUndoChanges int

	zoom       float64 // pixels per frame
	layerH     float64
	headerW    float64
	playheadX  float64
	draggingPH bool

	lastTick  time.Time
	playAccum float64

	drawingCircle    bool
	circleStartX     float64
	circleStartY     float64
	circleNowX       float64
	circleNowY       float64
	shapeSubtool     string
	drawingShape     bool
	shapeStartX      float64
	shapeStartY      float64
	shapeNowX        float64
	shapeNowY        float64
	shapeAsStar      bool
	shapeFromCenter  bool
	shapeUniform     bool
	shapeSides       int
	shapeToolFill    string
	shapeToolStroke  string
	activeColorField string
	activeColorBtn   js.Value
	colorPickerDirty bool

	penEditing   bool
	penPoints    []BezierPoint
	penMouseDown bool
	penDragIndex int
	penDragMoved bool
	penMouseX    float64
	penMouseY    float64

	selectedLayerIdx        int
	selectedInstIdx         int
	selectedInstances       map[string]bool
	selectedPathPt          int
	selectedHandle          string
	selectedTweenLayerIdx   int
	selectedTweenInstIdx    int
	selectedTweenStartFrame int
	selectedTweenEndFrame   int
	layerCtxTargetIdx       int
	stageCtxLayerIdx        int
	stageCtxInstIdx         int
	keyframeCtxLayerIdx     int
	keyframeCtxFrame        int
	selectedLibrarySymbolID string
	dragLibrarySymbolID     string
	dragLibraryClientX      float64
	dragLibraryClientY      float64
	dragLibraryStageX       float64
	dragLibraryStageY       float64
	dragLibraryOverStage    bool
	dragMode                string
	lastMouseX              float64
	lastMouseY              float64
	marqueeActive           bool
	marqueeStartX           float64
	marqueeStartY           float64
	marqueeNowX             float64
	marqueeNowY             float64
	marqueeAdditive         bool
	undoStack               []appSnapshot
	redoStack               []appSnapshot
	historyBatchOpen        bool
	suspendHistory          bool

	heldCallbacks []js.Func
}

func (a *App) holdCallback(fn js.Func) js.Func {
	a.heldCallbacks = append(a.heldCallbacks, fn)
	return fn
}

func cloneDocument(doc Document) Document {
	data, err := json.Marshal(doc)
	if err != nil {
		return doc
	}
	var copy Document
	if err := json.Unmarshal(data, &copy); err != nil {
		return doc
	}
	return copy
}

func cloneSelectionMap(src map[string]bool) map[string]bool {
	if src == nil {
		return map[string]bool{}
	}
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		if v {
			dst[k] = true
		}
	}
	return dst
}

func (a *App) makeSnapshot() appSnapshot {
	return appSnapshot{
		Doc:                     cloneDocument(a.doc),
		CurFrame:                a.curFrame,
		SelectedLayerIdx:        a.selectedLayerIdx,
		SelectedInstIdx:         a.selectedInstIdx,
		SelectedInstances:       cloneSelectionMap(a.selectedInstances),
		SelectedTweenLayerIdx:   a.selectedTweenLayerIdx,
		SelectedTweenInstIdx:    a.selectedTweenInstIdx,
		SelectedTweenStartFrame: a.selectedTweenStartFrame,
		SelectedTweenEndFrame:   a.selectedTweenEndFrame,
		SelectedLibrarySymbolID: a.selectedLibrarySymbolID,
	}
}

func (a *App) trimUndoStack() {
	if a.maxUndoChanges < 1 {
		a.maxUndoChanges = 1
	}
	if len(a.undoStack) > a.maxUndoChanges {
		a.undoStack = append([]appSnapshot(nil), a.undoStack[len(a.undoStack)-a.maxUndoChanges:]...)
	}
}

func (a *App) captureUndoSnapshot() {
	if a.suspendHistory {
		return
	}
	a.undoStack = append(a.undoStack, a.makeSnapshot())
	a.trimUndoStack()
	a.redoStack = nil
}

func (a *App) beginHistoryBatch() {
	if a.historyBatchOpen {
		return
	}
	a.captureUndoSnapshot()
	a.historyBatchOpen = true
}

func (a *App) endHistoryBatch() {
	a.historyBatchOpen = false
}

func (a *App) restoreSnapshot(s appSnapshot) {
	a.suspendHistory = true
	a.historyBatchOpen = false
	a.timelinePath = nil
	a.doc = cloneDocument(s.Doc)
	normalizeDocument(&a.doc)
	a.syncBitmapAssets()
	a.curFrame = s.CurFrame
	if a.curFrame < 1 {
		a.curFrame = 1
	}
	if a.curFrame > a.doc.TotalFrames {
		a.curFrame = a.doc.TotalFrames
	}
	a.selectedLayerIdx = -1
	a.selectedInstIdx = -1
	a.selectedInstances = make(map[string]bool)
	for key, selected := range s.SelectedInstances {
		if !selected {
			continue
		}
		li, ii, ok := parseSelKey(key)
		if !ok || li < 0 || li >= len(a.doc.Layers) || ii < 0 || ii >= len(a.doc.Layers[li].Instances) {
			continue
		}
		a.selectedInstances[key] = true
	}
	if s.SelectedLayerIdx >= 0 && s.SelectedLayerIdx < len(a.doc.Layers) &&
		s.SelectedInstIdx >= 0 && s.SelectedInstIdx < len(a.doc.Layers[s.SelectedLayerIdx].Instances) {
		a.selectedLayerIdx = s.SelectedLayerIdx
		a.selectedInstIdx = s.SelectedInstIdx
	}
	a.selectedTweenLayerIdx = -1
	a.selectedTweenInstIdx = -1
	a.selectedTweenStartFrame = -1
	a.selectedTweenEndFrame = -1
	if s.SelectedTweenLayerIdx >= 0 && s.SelectedTweenLayerIdx < len(a.doc.Layers) &&
		s.SelectedTweenInstIdx >= 0 && s.SelectedTweenInstIdx < len(a.doc.Layers[s.SelectedTweenLayerIdx].Instances) {
		a.selectedTweenLayerIdx = s.SelectedTweenLayerIdx
		a.selectedTweenInstIdx = s.SelectedTweenInstIdx
		a.selectedTweenStartFrame = s.SelectedTweenStartFrame
		a.selectedTweenEndFrame = s.SelectedTweenEndFrame
	}
	a.selectedLibrarySymbolID = s.SelectedLibrarySymbolID
	a.closeDocumentDialog()
	a.closeSettingsDialog()
	a.refreshDocUI()
	a.resizeCanvases()
	a.updatePropertiesPanel()
	a.suspendHistory = false
}

func (a *App) undo() {
	if len(a.undoStack) == 0 {
		a.statusEl.Set("textContent", "Nothing to undo")
		return
	}
	current := a.makeSnapshot()
	last := a.undoStack[len(a.undoStack)-1]
	a.undoStack = a.undoStack[:len(a.undoStack)-1]
	a.redoStack = append(a.redoStack, current)
	a.restoreSnapshot(last)
	a.statusEl.Set("textContent", "Undo")
}

func (a *App) redo() {
	if len(a.redoStack) == 0 {
		a.statusEl.Set("textContent", "Nothing to redo")
		return
	}
	current := a.makeSnapshot()
	next := a.redoStack[len(a.redoStack)-1]
	a.redoStack = a.redoStack[:len(a.redoStack)-1]
	a.undoStack = append(a.undoStack, current)
	a.trimUndoStack()
	a.restoreSnapshot(next)
	a.statusEl.Set("textContent", "Redo")
}

func main() {
	app := &App{
		doc:                     newDefaultDocument(),
		activeTool:              "select",
		shapeSubtool:            "oval",
		shapeSides:              5,
		shapeToolFill:           "#66e3ff",
		shapeToolStroke:         "#66e3ff",
		bitmapImages:            make(map[string]js.Value),
		curFrame:                1,
		autoKey:                 true,
		lockScale:               true,
		maxUndoChanges:          100,
		zoom:                    10,  // px per frame
		layerH:                  28,  // px
		headerW:                 180, // px
		selectedLayerIdx:        -1,
		selectedInstIdx:         -1,
		selectedInstances:       make(map[string]bool),
		selectedPathPt:          -1,
		selectedTweenLayerIdx:   -1,
		selectedTweenInstIdx:    -1,
		selectedTweenStartFrame: -1,
		selectedTweenEndFrame:   -1,
		layerCtxTargetIdx:       -1,
		stageCtxLayerIdx:        -1,
		stageCtxInstIdx:         -1,
		keyframeCtxLayerIdx:     -1,
		keyframeCtxFrame:        -1,
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
	a.stageViewport = d.Call("getElementById", "stageViewport")

	a.tlCanvas = d.Call("getElementById", "timeline")
	a.tlCtx = a.tlCanvas.Call("getContext", "2d")

	a.statusEl = d.Call("getElementById", "status")
	a.sceneNameEl = d.Call("getElementById", "sceneNamePill")
	a.stageTimelineEl = d.Call("getElementById", "stageTimelineBreadcrumb")
	a.docSizeEl = d.Call("getElementById", "docSize")
	a.docFpsEl = d.Call("getElementById", "docFps")
	a.propDocName = d.Call("getElementById", "propDocName")
	a.propDocWidth = d.Call("getElementById", "propDocWidth")
	a.propDocHeight = d.Call("getElementById", "propDocHeight")
	a.propDocFps = d.Call("getElementById", "propDocFps")
	a.propDocBg = d.Call("getElementById", "propDocBg")
	a.curFrameEl = d.Call("getElementById", "curFrame")
	a.isPlayEl = d.Call("getElementById", "isPlaying")
	a.selNameEl = d.Call("getElementById", "selName")
	a.selToolEl = d.Call("getElementById", "selTool")
	a.libraryListEl = d.Call("getElementById", "libraryList")
	a.propertiesPanelEl = d.Call("getElementById", "propertiesPanel")
	a.libraryPanelEl = d.Call("getElementById", "libraryPanel")
	a.propName = d.Call("getElementById", "propName")
	a.propPosX = d.Call("getElementById", "propPosX")
	a.propPosY = d.Call("getElementById", "propPosY")
	a.propScaleX = d.Call("getElementById", "propScaleX")
	a.propScaleY = d.Call("getElementById", "propScaleY")
	a.propScaleLock = d.Call("getElementById", "propScaleLock")
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
	a.propEaseMode = d.Call("getElementById", "propEaseMode")
	a.propEaseDir = d.Call("getElementById", "propEaseDir")
	a.layerCtxMenu = d.Call("getElementById", "layerContextMenu")
	a.stageCtxMenu = d.Call("getElementById", "stageContextMenu")
	a.keyframeCtxMenu = d.Call("getElementById", "keyframeContextMenu")
	a.autoKeyBtn = d.Call("getElementById", "btn-autokey")
	a.docDialog = d.Call("getElementById", "docDialog")
	a.docDlgName = d.Call("getElementById", "docDialogName")
	a.docDlgWidth = d.Call("getElementById", "docDialogWidth")
	a.docDlgHeight = d.Call("getElementById", "docDialogHeight")
	a.docDlgFps = d.Call("getElementById", "docDialogFps")
	a.docDlgBg = d.Call("getElementById", "docDialogBg")
	a.docDlgCancel = d.Call("getElementById", "docDialogCancel")
	a.docDlgSave = d.Call("getElementById", "docDialogSave")
	a.settingsDialog = d.Call("getElementById", "settingsDialog")
	a.settingsMaxUndo = d.Call("getElementById", "settingsMaxUndo")
	a.settingsDlgCancel = d.Call("getElementById", "settingsDialogCancel")
	a.settingsDlgSave = d.Call("getElementById", "settingsDialogSave")
	a.shapeToolBtn = d.Call("getElementById", "btn-square")
	a.shapeToolIconEl = d.Call("getElementById", "shapeToolIcon")
	a.shapeToolCorner = d.Call("getElementById", "shapeToolCorner")
	a.shapeToolMenu = d.Call("getElementById", "shapeToolMenu")
	a.toolFill = d.Call("getElementById", "toolFill")
	a.toolStroke = d.Call("getElementById", "toolStroke")
	a.colorPickerPopover = d.Call("getElementById", "colorPickerPopover")
	a.colorPickerPreview = d.Call("getElementById", "colorPickerPreview")
	a.colorPickerPreviewText = d.Call("getElementById", "colorPickerPreviewText")
	a.colorPickerColor = d.Call("getElementById", "colorPickerColor")
	a.colorPickerAlpha = d.Call("getElementById", "colorPickerAlpha")
	a.colorPickerAlphaValue = d.Call("getElementById", "colorPickerAlphaValue")

	a.statusEl.Set("textContent", "WASM ready")
	a.syncBitmapAssets()
	a.refreshDocUI()
	a.updateAutoKeyUI()
	a.updateScaleLockUI()
	a.updateShapeToolUI()
}

func (a *App) refreshDocUI() {
	if a.sceneNameEl.Truthy() {
		a.sceneNameEl.Set("textContent", a.doc.Name)
	}
	if a.stageTimelineEl.Truthy() {
		a.stageTimelineEl.Set("textContent", a.currentTimelineBreadcrumb())
	}
	js.Global().Get("document").Set("title", fmt.Sprintf("%s - Animate-like Editor (Go WASM)", a.doc.Name))
	if a.docSizeEl.Truthy() {
		a.docSizeEl.Set("textContent", fmt.Sprintf("%d x %d px", a.doc.Width, a.doc.Height))
	}
	if a.docFpsEl.Truthy() {
		a.docFpsEl.Set("textContent", fmt.Sprintf("%d", a.doc.FPS))
	}
	if a.stageCanvas.Truthy() {
		a.stageCanvas.Get("style").Set("aspectRatio", fmt.Sprintf("%d / %d", a.doc.Width, a.doc.Height))
	}
	a.updateSelectedLayerLabel()
	a.updateLibraryPanel()
}

func (a *App) updateAutoKeyUI() {
	if !a.autoKeyBtn.Truthy() {
		return
	}
	if a.autoKey {
		a.autoKeyBtn.Get("classList").Call("add", "active")
	} else {
		a.autoKeyBtn.Get("classList").Call("remove", "active")
	}
}

func (a *App) closeShapeToolMenu() {
	if a.shapeToolMenu.Truthy() {
		a.shapeToolMenu.Get("classList").Call("remove", "open")
	}
}

func (a *App) openShapeToolMenu() {
	if a.shapeToolMenu.Truthy() {
		a.shapeToolMenu.Get("classList").Call("add", "open")
	}
}

func (a *App) setShapeSubtool(subtool string) {
	switch subtool {
	case "oval":
		a.shapeSubtool = "oval"
	case "polygon":
		a.shapeSubtool = "polygon"
	default:
		a.shapeSubtool = "rectangle"
	}
	a.updateShapeToolUI()
}

func (a *App) updateShapeToolUI() {
	if a.shapeToolIconEl.Truthy() {
		if a.shapeSubtool == "oval" {
			a.shapeToolIconEl.Set("innerHTML", "&#x25EF;")
		} else if a.shapeSubtool == "polygon" {
			a.shapeToolIconEl.Set("innerHTML", "&#x2B20;")
		} else {
			a.shapeToolIconEl.Set("innerHTML", "&#x25AD;")
		}
	}
	d := js.Global().Get("document")
	if btn := d.Call("getElementById", "btn-shape"); btn.Truthy() {
		if a.shapeSubtool == "oval" {
			btn.Set("textContent", "Oval")
		} else if a.shapeSubtool == "polygon" {
			btn.Set("textContent", "Polygon")
		} else {
			btn.Set("textContent", "Rectangle")
		}
	}
	if !a.shapeToolMenu.Truthy() {
		return
	}
	items := a.shapeToolMenu.Call("querySelectorAll", "[data-shape-subtool]")
	for i := 0; i < items.Length(); i++ {
		item := items.Index(i)
		if item.Get("dataset").Get("shapeSubtool").String() == a.shapeSubtool {
			item.Get("classList").Call("add", "active")
		} else {
			item.Get("classList").Call("remove", "active")
		}
	}
}

func (a *App) setRightPanelTab(name string) {
	d := js.Global().Get("document")
	tabs := d.Call("querySelectorAll", ".tab")
	for i := 0; i < tabs.Length(); i++ {
		tab := tabs.Index(i)
		if tab.Get("dataset").Get("panel").String() == name {
			tab.Get("classList").Call("add", "active")
		} else {
			tab.Get("classList").Call("remove", "active")
		}
	}
	if a.propertiesPanelEl.Truthy() {
		if name == "properties" {
			a.propertiesPanelEl.Get("classList").Call("add", "active")
		} else {
			a.propertiesPanelEl.Get("classList").Call("remove", "active")
		}
	}
	if a.libraryPanelEl.Truthy() {
		if name == "library" {
			a.libraryPanelEl.Get("classList").Call("add", "active")
		} else {
			a.libraryPanelEl.Get("classList").Call("remove", "active")
		}
	}
}

func (a *App) activatePropertiesPanelForSelection() {
	a.setRightPanelTab("properties")
	if a.propertiesPanelEl.Truthy() {
		a.propertiesPanelEl.Call("setAttribute", "tabindex", "-1")
		a.propertiesPanelEl.Call("focus")
	}
}

func (a *App) updateLibraryPanel() {
	if !a.libraryListEl.Truthy() {
		return
	}
	a.libraryListEl.Set("innerHTML", "")
	d := js.Global().Get("document")
	if len(a.doc.Symbols) == 0 {
		empty := d.Call("createElement", "div")
		empty.Set("className", "libraryMeta")
		empty.Set("textContent", "No symbols yet")
		a.libraryListEl.Call("appendChild", empty)
		return
	}
	for _, sym := range a.doc.Symbols {
		sym := sym
		item := d.Call("createElement", "div")
		item.Set("className", "libraryItem")
		if sym.ID == a.selectedLibrarySymbolID {
			item.Get("classList").Call("add", "selected")
		}
		item.Get("dataset").Set("symbolId", sym.ID)
		name := d.Call("createElement", "div")
		name.Set("className", "libraryName")
		name.Set("textContent", sym.Name)
		renameCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			e := args[0]
			if e.Get("detail").Int() < 2 {
				return nil
			}
			e.Call("preventDefault")
			e.Call("stopPropagation")
			a.selectedLibrarySymbolID = sym.ID
			a.dragLibrarySymbolID = ""
			input := d.Call("createElement", "input")
			input.Set("className", "libraryRenameInput")
			input.Set("type", "text")
			input.Set("value", sym.Name)
			name.Set("innerHTML", "")
			name.Call("appendChild", input)
			committed := false
			var commitRename func()
			commitRename = func() {
				if committed {
					return
				}
				committed = true
				a.renameSymbol(sym.ID, input.Get("value").String())
			}
			blurCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				commitRename()
				return nil
			})
			keyCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				if len(args) == 0 {
					return nil
				}
				e := args[0]
				key := e.Get("key").String()
				if key == "Enter" {
					e.Call("preventDefault")
					commitRename()
				} else if key == "Escape" {
					e.Call("preventDefault")
					committed = true
					a.updateLibraryPanel()
				}
				return nil
			})
			stopMouseCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				if len(args) > 0 {
					args[0].Call("stopPropagation")
				}
				return nil
			})
			a.holdCallback(blurCb)
			a.holdCallback(keyCb)
			a.holdCallback(stopMouseCb)
			input.Call("addEventListener", "blur", blurCb)
			input.Call("addEventListener", "keydown", keyCb)
			input.Call("addEventListener", "mousedown", stopMouseCb)
			input.Call("focus")
			input.Call("select")
			return nil
		})
		a.holdCallback(renameCb)
		name.Call("addEventListener", "mousedown", renameCb)
		meta := d.Call("createElement", "div")
		meta.Set("className", "libraryMeta")
		if sym.SymbolType == "bitmap" {
			meta.Set("textContent", fmt.Sprintf("Bitmap, %.0f x %.0f", sym.BitmapW, sym.BitmapH))
		} else {
			nestedCount := len(sym.Instances)
			for _, layer := range sym.Layers {
				nestedCount += len(layer.Instances)
			}
			meta.Set("textContent", fmt.Sprintf("%s, %d nested instance(s)", strings.Title(sym.SymbolType), nestedCount))
		}
		item.Call("appendChild", name)
		item.Call("appendChild", meta)
		mouseDownCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			e := args[0]
			a.selectedLibrarySymbolID = sym.ID
			a.dragLibrarySymbolID = sym.ID
			a.dragLibraryClientX = e.Get("clientX").Float()
			a.dragLibraryClientY = e.Get("clientY").Float()
			a.dragLibraryOverStage = false
			a.updateLibraryPanel()
			return nil
		})
		a.holdCallback(mouseDownCb)
		item.Call("addEventListener", "mousedown", mouseDownCb)
		a.libraryListEl.Call("appendChild", item)
	}
}

func isRenderableInstanceType(inst ElementInstance) bool {
	return inst.ElementType == "path" || inst.ElementType == "circle" || inst.ElementType == "symbol"
}

func (a *App) symbolNameByID(id string) string {
	if sym, ok := a.findSymbolByID(id); ok {
		return sym.Name
	}
	return "Symbol"
}

func (a *App) renameSymbol(symbolID, newName string) {
	newName = strings.TrimSpace(newName)
	if newName == "" {
		a.updateLibraryPanel()
		return
	}
	for i := range a.doc.Symbols {
		if a.doc.Symbols[i].ID != symbolID {
			continue
		}
		if a.doc.Symbols[i].Name == newName {
			a.updateLibraryPanel()
			return
		}
		a.captureUndoSnapshot()
		a.doc.Symbols[i].Name = newName
		a.updateLibraryPanel()
		a.statusEl.Set("textContent", fmt.Sprintf("Renamed symbol to %s", newName))
		return
	}
	a.updateLibraryPanel()
}

func (a *App) updateLibraryDragPosition(clientX, clientY float64) {
	a.dragLibraryClientX = clientX
	a.dragLibraryClientY = clientY
	a.dragLibraryOverStage = false
	if !a.stageCanvas.Truthy() || a.dragLibrarySymbolID == "" {
		return
	}
	rect := a.stageCanvas.Call("getBoundingClientRect")
	left := rect.Get("left").Float()
	top := rect.Get("top").Float()
	width := rect.Get("width").Float()
	height := rect.Get("height").Float()
	stageX := clientX - left
	stageY := clientY - top
	if stageX >= 0 && stageX <= width && stageY >= 0 && stageY <= height {
		a.dragLibraryOverStage = true
		a.dragLibraryStageX = stageX
		a.dragLibraryStageY = stageY
	}
}

func (a *App) clearLibraryDrag() {
	a.dragLibrarySymbolID = ""
	a.dragLibraryOverStage = false
}

func (a *App) addSymbolInstanceAsNewLayer(symbolID string, x, y float64) {
	sym, ok := a.findSymbolByID(symbolID)
	if !ok {
		return
	}
	a.captureUndoSnapshot()
	layers := a.currentLayersPtr()
	for i := range *layers {
		(*layers)[i].Selected = false
	}
	layerIdx := 0
	layerName := sym.Name
	layer := Layer{
		Name:        layerName,
		Description: "Layer created from Library symbol",
		Color:       "#c77dff",
		Selected:    true,
		Instances:   []ElementInstance{},
	}
	kf := defaultKeyframeAt(a.curFrame)
	if minX, minY, maxX, maxY, ok := a.symbolBoundsAtFrame(sym, a.curFrame); ok {
		kf.AnchorX = (minX + maxX) / 2
		kf.AnchorY = (minY + maxY) / 2
		kf.X = x - kf.AnchorX
		kf.Y = y - kf.AnchorY
	} else {
		kf.X = x
		kf.Y = y
	}
	inst := ElementInstance{
		ID:          fmt.Sprintf("layer-%d-symbol-instance-1", len(*layers)+1),
		Name:        "",
		Description: "Symbol instance from Library",
		ElementType: "symbol",
		ElementID:   symbolID,
		Keyframes:   map[int]InstanceKeyframe{a.curFrame: kf},
	}
	layer.Instances = append(layer.Instances, inst)
	*layers = append([]Layer{layer}, (*layers)...)
	a.setSingleInstanceSelection(layerIdx, 0)
	a.selectedLibrarySymbolID = symbolID
	a.updateSelectedLayerLabel()
	a.updateLibraryPanel()
	a.statusEl.Set("textContent", "Symbol placed on new layer")
}

func baseNameWithoutExt(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Bitmap"
	}
	if idx := strings.LastIndex(name, "."); idx > 0 {
		name = name[:idx]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "Bitmap"
	}
	return name
}

func (a *App) createTopLayer(name, description string) int {
	layers := a.currentLayersPtr()
	for i := range *layers {
		(*layers)[i].Selected = false
	}
	layer := Layer{
		Name:        name,
		Description: description,
		Color:       "#c77dff",
		Selected:    true,
		Instances:   []ElementInstance{},
	}
	*layers = append([]Layer{layer}, (*layers)...)
	return 0
}

func (a *App) targetLayerForImportedBitmap(symbolName string) int {
	layers := a.currentLayers()
	if a.selectedLayerIdx >= 0 && a.selectedLayerIdx < len(layers) {
		return a.selectedLayerIdx
	}
	for i := range layers {
		if layers[i].Selected {
			return i
		}
	}
	return a.createTopLayer(symbolName, "Layer created for imported bitmap")
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
	if doc.Background == "" {
		doc.Background = "#808080"
	} else {
		doc.Background = normalizeHexColor(doc.Background)
	}
	if doc.TotalFrames <= 0 {
		doc.TotalFrames = 120
	}

	for li := range doc.Layers {
		layer := &doc.Layers[li]
		if layer.Color == "" {
			layer.Color = "#c77dff"
		}
		for ii := range layer.Instances {
			inst := &layer.Instances[ii]
			if inst.ID == "" {
				inst.ID = fmt.Sprintf("layer-%d-instance-%d", li+1, ii+1)
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
				kf.EaseMode = normalizeEaseMode(kf.EaseMode)
				kf.EaseDir = normalizeEaseDir(kf.EaseDir)
				inst.Keyframes[frame] = kf
			}
		}
	}
	if doc.Symbols == nil {
		doc.Symbols = []Symbol{}
	}
	for si := range doc.Symbols {
		sym := &doc.Symbols[si]
		if sym.ID == "" {
			sym.ID = fmt.Sprintf("symbol-%d", si+1)
		}
		if sym.Name == "" {
			if sym.SymbolType == "bitmap" {
				sym.Name = fmt.Sprintf("Bitmap %d", si+1)
			} else {
				sym.Name = fmt.Sprintf("Movie Clip %d", si+1)
			}
		}
		if sym.SymbolType == "" {
			sym.SymbolType = "movieclip"
		}
		if sym.SymbolType == "bitmap" {
			if sym.BitmapW < 0 {
				sym.BitmapW = -sym.BitmapW
			}
			if sym.BitmapH < 0 {
				sym.BitmapH = -sym.BitmapH
			}
		} else if sym.SymbolType == "movieclip" && len(sym.Layers) == 0 && len(sym.Instances) > 0 {
			sym.Layers = []Layer{{
				Name:        "Layer 1",
				Description: "Migrated MovieClip layer",
				Color:       "#c77dff",
				Selected:    true,
				Instances:   append([]ElementInstance(nil), sym.Instances...),
			}}
		}
		if sym.SymbolType == "movieclip" && sym.Layers == nil {
			sym.Layers = []Layer{}
		}
		if sym.Instances == nil {
			sym.Instances = []ElementInstance{}
		}
		for li := range sym.Layers {
			layer := &sym.Layers[li]
			if layer.Color == "" {
				layer.Color = "#c77dff"
			}
			for ii := range layer.Instances {
				inst := &layer.Instances[ii]
				if inst.ID == "" {
					inst.ID = fmt.Sprintf("%s-layer-%d-instance-%d", sym.ID, li+1, ii+1)
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
					kf.EaseMode = normalizeEaseMode(kf.EaseMode)
					kf.EaseDir = normalizeEaseDir(kf.EaseDir)
					inst.Keyframes[frame] = kf
				}
			}
		}
		for ii := range sym.Instances {
			inst := &sym.Instances[ii]
			if inst.ID == "" {
				inst.ID = fmt.Sprintf("%s-instance-%d", sym.ID, ii+1)
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
				kf.EaseMode = normalizeEaseMode(kf.EaseMode)
				kf.EaseDir = normalizeEaseDir(kf.EaseDir)
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

func (a *App) nextSymbolID() string {
	return fmt.Sprintf("symbol-%d", len(a.doc.Symbols)+1)
}

func (a *App) nextPathID() string {
	return fmt.Sprintf("path-%d", len(a.doc.Paths)+1)
}

func (a *App) selectedLayerIndexes() []int {
	layers := a.currentLayersPtr()
	selected := make([]int, 0, len(*layers))
	for i := range *layers {
		if (*layers)[i].Selected {
			selected = append(selected, i)
		}
	}
	if len(selected) == 0 && len(*layers) > 0 {
		(*layers)[0].Selected = true
		selected = append(selected, 0)
	}
	return selected
}

func (a *App) updateSelectedLayerLabel() {
	pairs := a.selectedInstancePairs()
	if len(pairs) == 1 {
		li, ii := pairs[0][0], pairs[0][1]
		layers := a.currentLayers()
		a.selNameEl.Set("textContent", a.instanceDisplayName(layers[li].Instances[ii]))
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
	layers := a.currentLayers()
	for _, idx := range selected {
		names = append(names, layers[idx].Name)
	}
	a.selNameEl.Set("textContent", strings.Join(names, ", "))
}

func (a *App) instanceDisplayName(inst ElementInstance) string {
	if strings.TrimSpace(inst.Name) != "" {
		return inst.Name
	}
	switch inst.ElementType {
	case "path":
		return "Path"
	case "circle":
		return "Circle"
	case "symbol":
		if inst.ElementID != "" {
			return a.symbolNameByID(inst.ElementID)
		}
		return "Movie Clip"
	default:
		return "Instance"
	}
}

func (a *App) selectLayer(layerIdx int, additive bool) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return
	}
	if additive {
		(*layers)[layerIdx].Selected = !(*layers)[layerIdx].Selected
	} else {
		for i := range *layers {
			(*layers)[i].Selected = i == layerIdx
		}
	}
	a.updateSelectedLayerLabel()
}

func (a *App) addPathInstanceToSelectedLayers(pathID string, baseKeyframe InstanceKeyframe) {
	selected := a.selectedLayerIndexes()
	layers := a.currentLayersPtr()
	for _, layerIdx := range selected {
		layer := &(*layers)[layerIdx]
		n := len(layer.Instances) + 1
		inst := ElementInstance{
			ID:          fmt.Sprintf("layer-%d-path-instance-%d", layerIdx+1, n),
			Name:        "",
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
			Name:        "",
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

func bezierCornerPoint(x, y float64) BezierPoint {
	return BezierPoint{
		X:    x,
		Y:    y,
		InX:  x,
		InY:  y,
		OutX: x,
		OutY: y,
	}
}

func rectanglePathPoints(x0, y0, x1, y1 float64, fromCenter, uniform bool) ([]BezierPoint, float64, float64, bool) {
	cx := (x0 + x1) / 2
	cy := (y0 + y1) / 2
	halfW := math.Abs(x1-x0) / 2
	halfH := math.Abs(y1-y0) / 2
	if fromCenter {
		cx = x0
		cy = y0
		halfW = math.Abs(x1 - x0)
		halfH = math.Abs(y1 - y0)
	}
	if uniform {
		size := math.Max(halfW, halfH)
		halfW = size
		halfH = size
	}
	if halfW < 1 || halfH < 1 {
		return nil, 0, 0, false
	}
	points := []BezierPoint{
		bezierCornerPoint(-halfW, -halfH),
		bezierCornerPoint(halfW, -halfH),
		bezierCornerPoint(halfW, halfH),
		bezierCornerPoint(-halfW, halfH),
	}
	return points, cx, cy, true
}

func ovalPathPoints(x0, y0, x1, y1 float64, fromCenter, uniform bool) ([]BezierPoint, float64, float64, bool) {
	cx := (x0 + x1) / 2
	cy := (y0 + y1) / 2
	rx := math.Abs(x1-x0) / 2
	ry := math.Abs(y1-y0) / 2
	if fromCenter {
		cx = x0
		cy = y0
		rx = math.Abs(x1 - x0)
		ry = math.Abs(y1 - y0)
	}
	if uniform {
		r := math.Max(rx, ry)
		rx = r
		ry = r
	}
	if rx < 1 || ry < 1 {
		return nil, 0, 0, false
	}
	k := 0.5522847498307936
	points := []BezierPoint{
		{X: 0, Y: -ry, InX: -k * rx, InY: -ry, OutX: k * rx, OutY: -ry},
		{X: rx, Y: 0, InX: rx, InY: -k * ry, OutX: rx, OutY: k * ry},
		{X: 0, Y: ry, InX: k * rx, InY: ry, OutX: -k * rx, OutY: ry},
		{X: -rx, Y: 0, InX: -rx, InY: k * ry, OutX: -rx, OutY: -k * ry},
	}
	return points, cx, cy, true
}

func polygonPathPoints(x0, y0, x1, y1 float64, sides int, star bool, fromCenter, uniform bool) ([]BezierPoint, float64, float64, bool) {
	if sides < 3 {
		sides = 3
	}
	if sides > 100 {
		sides = 100
	}
	cx := (x0 + x1) / 2
	cy := (y0 + y1) / 2
	rx := math.Abs(x1-x0) / 2
	ry := math.Abs(y1-y0) / 2
	if fromCenter {
		cx = x0
		cy = y0
		rx = math.Abs(x1 - x0)
		ry = math.Abs(y1 - y0)
	}
	if uniform {
		r := math.Max(rx, ry)
		rx = r
		ry = r
	}
	if rx < 2 || ry < 2 {
		return nil, 0, 0, false
	}
	count := sides
	if star {
		count = sides * 2
	}
	points := make([]BezierPoint, 0, count)
	innerScale := 0.45
	for i := 0; i < count; i++ {
		angle := -math.Pi/2 + (float64(i)*2*math.Pi)/float64(count)
		scale := 1.0
		if star && i%2 == 1 {
			scale = innerScale
		}
		px := math.Cos(angle) * rx * scale
		py := math.Sin(angle) * ry * scale
		points = append(points, bezierCornerPoint(px, py))
	}
	return points, cx, cy, true
}

func (a *App) commitShapeDraft() {
	if !a.drawingShape {
		return
	}
	var (
		points []BezierPoint
		cx     float64
		cy     float64
		ok     bool
	)
	switch a.shapeSubtool {
	case "oval":
		points, cx, cy, ok = ovalPathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeFromCenter, a.shapeUniform)
	case "polygon":
		points, cx, cy, ok = polygonPathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeSides, a.shapeAsStar, a.shapeFromCenter, a.shapeUniform)
	default:
		points, cx, cy, ok = rectanglePathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeFromCenter, a.shapeUniform)
	}
	if !ok {
		a.drawingShape = false
		return
	}
	a.captureUndoSnapshot()
	pathID := a.nextPathID()
	path := VectorPath{
		ID:      pathID,
		Points:  points,
		Stroke:  a.shapeToolStroke,
		Fill:    a.shapeToolFill,
		StrokeW: 2,
		Closed:  true,
	}
	a.doc.Paths = append(a.doc.Paths, path)
	kf := defaultKeyframeAt(a.curFrame)
	kf.X = cx
	kf.Y = cy
	kf.AnchorX = 0
	kf.AnchorY = 0
	a.addPathInstanceToSelectedLayers(pathID, kf)
	if a.shapeSubtool == "polygon" {
		if a.shapeAsStar {
			a.statusEl.Set("textContent", fmt.Sprintf("Star created with %d points", a.shapeSides))
		} else {
			a.statusEl.Set("textContent", fmt.Sprintf("Polygon created with %d sides", a.shapeSides))
		}
	} else if a.shapeSubtool == "oval" {
		a.statusEl.Set("textContent", "Oval created")
	} else {
		a.statusEl.Set("textContent", "Rectangle created")
	}
	a.drawingShape = false
}

func resolveElementInstanceKeyframe(inst ElementInstance, frame int, totalFrames int) (InstanceKeyframe, bool) {
	if exact, ok := inst.Keyframes[frame]; ok {
		exact.Frame = frame
		exact.EaseMode = normalizeEaseMode(exact.EaseMode)
		exact.EaseDir = normalizeEaseDir(exact.EaseDir)
		return exact, true
	}
	prevFound := false
	nextFound := false
	best := -1
	next := totalFrames + 1
	for f := range inst.Keyframes {
		if f <= frame && f > best {
			best = f
			prevFound = true
		}
		if f > frame && f < next {
			next = f
			nextFound = true
		}
	}
	if !prevFound && !nextFound {
		return InstanceKeyframe{}, false
	}
	if !prevFound {
		kf := inst.Keyframes[next]
		kf.Frame = frame
		kf.EaseMode = normalizeEaseMode(kf.EaseMode)
		kf.EaseDir = normalizeEaseDir(kf.EaseDir)
		return kf, true
	}
	prev := inst.Keyframes[best]
	prev.EaseMode = normalizeEaseMode(prev.EaseMode)
	prev.EaseDir = normalizeEaseDir(prev.EaseDir)
	if !nextFound {
		prev.Frame = frame
		return prev, true
	}
	if next <= best {
		prev.Frame = frame
		return prev, true
	}
	nextKF := inst.Keyframes[next]
	t := float64(frame-best) / float64(next-best)
	t = easeValue(t, prev.EaseMode, prev.EaseDir)
	kf := InstanceKeyframe{
		Frame:    frame,
		X:        lerpFloat(prev.X, nextKF.X, t),
		Y:        lerpFloat(prev.Y, nextKF.Y, t),
		ScaleX:   lerpFloat(prev.ScaleX, nextKF.ScaleX, t),
		ScaleY:   lerpFloat(prev.ScaleY, nextKF.ScaleY, t),
		SkewX:    lerpFloat(prev.SkewX, nextKF.SkewX, t),
		SkewY:    lerpFloat(prev.SkewY, nextKF.SkewY, t),
		Rotation: lerpFloat(prev.Rotation, nextKF.Rotation, t),
		AnchorX:  lerpFloat(prev.AnchorX, nextKF.AnchorX, t),
		AnchorY:  lerpFloat(prev.AnchorY, nextKF.AnchorY, t),
		Opacity:  lerpFloat(prev.Opacity, nextKF.Opacity, t),
		EaseMode: prev.EaseMode,
		EaseDir:  prev.EaseDir,
	}
	return kf, true
}

func (a *App) getInstanceKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) {
		return InstanceKeyframe{}, false
	}
	layer := layers[layerIdx]
	if instIdx < 0 || instIdx >= len(layer.Instances) {
		return InstanceKeyframe{}, false
	}
	return resolveElementInstanceKeyframe(layer.Instances[instIdx], frame, a.doc.TotalFrames)
}

func (a *App) getOrCreateInstanceKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return InstanceKeyframe{}, false
	}
	if instIdx < 0 || instIdx >= len((*layers)[layerIdx].Instances) {
		return InstanceKeyframe{}, false
	}
	inst := &(*layers)[layerIdx].Instances[instIdx]
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

func (a *App) getExactInstanceKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) {
		return InstanceKeyframe{}, false
	}
	if instIdx < 0 || instIdx >= len(layers[layerIdx].Instances) {
		return InstanceKeyframe{}, false
	}
	kf, ok := layers[layerIdx].Instances[instIdx].Keyframes[frame]
	return kf, ok
}

func (a *App) setInstanceKeyframe(layerIdx, instIdx, frame int, kf InstanceKeyframe) bool {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return false
	}
	if instIdx < 0 || instIdx >= len((*layers)[layerIdx].Instances) {
		return false
	}
	kf.Frame = frame
	(*layers)[layerIdx].Instances[instIdx].Keyframes[frame] = kf
	return true
}

func (a *App) writableTransformKeyframe(layerIdx, instIdx, frame int) (InstanceKeyframe, bool) {
	if kf, ok := a.getExactInstanceKeyframe(layerIdx, instIdx, frame); ok {
		return kf, true
	}
	if !a.autoKey {
		return InstanceKeyframe{}, false
	}
	a.addKeyframe(layerIdx, instIdx, frame)
	return a.getExactInstanceKeyframe(layerIdx, instIdx, frame)
}

func (a *App) clearInstanceSelection() {
	a.selectedLayerIdx = -1
	a.selectedInstIdx = -1
	a.selectedInstances = make(map[string]bool)
	a.selectedPathPt = -1
	a.selectedHandle = ""
	a.clearTweenSelection()
}

func (a *App) clearAllSelection() {
	a.clearInstanceSelection()
	layers := a.currentLayersPtr()
	for i := range *layers {
		(*layers)[i].Selected = false
	}
	a.updateSelectedLayerLabel()
	a.activatePropertiesPanelForSelection()
	a.updatePropertiesPanel()
}

func (a *App) clearTweenSelection() {
	a.selectedTweenLayerIdx = -1
	a.selectedTweenInstIdx = -1
	a.selectedTweenStartFrame = -1
	a.selectedTweenEndFrame = -1
}

func (a *App) closeLayerContextMenu() {
	if a.layerCtxMenu.Truthy() {
		a.layerCtxMenu.Get("classList").Call("remove", "open")
	}
	a.layerCtxTargetIdx = -1
}

func (a *App) closeStageContextMenu() {
	if a.stageCtxMenu.Truthy() {
		a.stageCtxMenu.Get("classList").Call("remove", "open")
	}
	a.stageCtxLayerIdx = -1
	a.stageCtxInstIdx = -1
}

func (a *App) closeKeyframeContextMenu() {
	if a.keyframeCtxMenu.Truthy() {
		a.keyframeCtxMenu.Get("classList").Call("remove", "open")
	}
	a.keyframeCtxLayerIdx = -1
	a.keyframeCtxFrame = -1
}

func (a *App) openLayerContextMenu(layerIdx int, clientX, clientY float64) {
	if layerIdx < 0 || layerIdx >= len(a.currentLayers()) || !a.layerCtxMenu.Truthy() {
		return
	}
	a.layerCtxTargetIdx = layerIdx
	a.layerCtxMenu.Get("style").Set("left", fmt.Sprintf("%.0fpx", clientX))
	a.layerCtxMenu.Get("style").Set("top", fmt.Sprintf("%.0fpx", clientY))
	a.layerCtxMenu.Get("classList").Call("add", "open")
}

func (a *App) openStageContextMenu(layerIdx, instIdx int, clientX, clientY float64) {
	if layerIdx < 0 || instIdx < 0 || !a.stageCtxMenu.Truthy() {
		return
	}
	a.stageCtxLayerIdx = layerIdx
	a.stageCtxInstIdx = instIdx
	a.stageCtxMenu.Get("style").Set("left", fmt.Sprintf("%.0fpx", clientX))
	a.stageCtxMenu.Get("style").Set("top", fmt.Sprintf("%.0fpx", clientY))
	a.stageCtxMenu.Get("classList").Call("add", "open")
}

func (a *App) openKeyframeContextMenu(layerIdx, frame int, clientX, clientY float64) {
	if layerIdx < 0 || layerIdx >= len(a.currentLayers()) || frame < 1 || frame > a.doc.TotalFrames || !a.keyframeCtxMenu.Truthy() {
		return
	}
	a.keyframeCtxLayerIdx = layerIdx
	a.keyframeCtxFrame = frame
	a.keyframeCtxMenu.Get("style").Set("left", fmt.Sprintf("%.0fpx", clientX))
	a.keyframeCtxMenu.Get("style").Set("top", fmt.Sprintf("%.0fpx", clientY))
	a.keyframeCtxMenu.Get("classList").Call("add", "open")
}

func (a *App) pickTimelineKeyframeAt(x, y float64) (int, int, bool) {
	if x <= a.headerW {
		return -1, -1, false
	}
	rowTop := 10.0 + 22.0 - 18.0
	layerIdx := int((y - rowTop) / a.layerH)
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) {
		return -1, -1, false
	}
	frame := a.xToFrame(x)
	keyX := a.frameToX(frame)
	keyW := math.Max(6, a.zoom-4)
	if x < keyX+2 || x > keyX+2+keyW {
		return -1, -1, false
	}
	if !layers[layerIdx].hasKeyframe(frame) {
		return -1, -1, false
	}
	return layerIdx, frame, true
}

func (a *App) deleteKeyframe(layerIdx, frame int) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) || frame < 1 || frame > a.doc.TotalFrames {
		return
	}
	removed := 0
	for _, inst := range (*layers)[layerIdx].Instances {
		if _, ok := inst.Keyframes[frame]; ok {
			removed++
		}
	}
	if removed == 0 {
		a.statusEl.Set("textContent", "No keyframe found to delete")
		return
	}
	a.captureUndoSnapshot()
	for ii := range (*layers)[layerIdx].Instances {
		delete((*layers)[layerIdx].Instances[ii].Keyframes, frame)
	}
	if a.selectedTweenLayerIdx == layerIdx && (a.selectedTweenStartFrame == frame || a.selectedTweenEndFrame == frame) {
		a.clearTweenSelection()
	}
	a.updatePropertiesPanel()
	a.statusEl.Set("textContent", fmt.Sprintf("Deleted keyframe at %d", frame))
}

func (a *App) renameLayer(layerIdx int) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return
	}
	current := (*layers)[layerIdx].Name
	next := js.Global().Call("prompt", "Rename layer", current)
	if !next.Truthy() {
		return
	}
	name := strings.TrimSpace(next.String())
	if name == "" || name == current {
		return
	}
	a.captureUndoSnapshot()
	(*layers)[layerIdx].Name = name
	a.updateSelectedLayerLabel()
	a.statusEl.Set("textContent", "Layer renamed")
}

func (a *App) deleteLayer(layerIdx int) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return
	}
	a.captureUndoSnapshot()

	if len(*layers) == 1 {
		(*layers)[0] = Layer{
			Name:        "Layer 1",
			Description: "Default empty layer",
			Color:       "#c77dff",
			Selected:    true,
			Instances:   []ElementInstance{},
		}
		a.clearInstanceSelection()
		a.updateSelectedLayerLabel()
		a.statusEl.Set("textContent", "Layer cleared")
		return
	}

	*layers = append((*layers)[:layerIdx], (*layers)[layerIdx+1:]...)
	for i := range *layers {
		(*layers)[i].Selected = false
	}
	nextIdx := layerIdx
	if nextIdx >= len(*layers) {
		nextIdx = len(*layers) - 1
	}
	if nextIdx >= 0 {
		(*layers)[nextIdx].Selected = true
	}
	a.clearInstanceSelection()
	a.updateSelectedLayerLabel()
	a.statusEl.Set("textContent", "Layer deleted")
}

func (a *App) deleteInstance(layerIdx, instIdx int) {
	layers := a.currentLayersPtr()
	if layerIdx < 0 || layerIdx >= len(*layers) {
		return
	}
	if instIdx < 0 || instIdx >= len((*layers)[layerIdx].Instances) {
		return
	}
	a.captureUndoSnapshot()
	layer := &(*layers)[layerIdx]
	layer.Instances = append(layer.Instances[:instIdx], layer.Instances[instIdx+1:]...)
	a.clearInstanceSelection()
	a.updateSelectedLayerLabel()
	a.statusEl.Set("textContent", "Instance deleted")
}

func (a *App) closeDocumentDialog() {
	if a.docDialog.Truthy() {
		a.docDialog.Get("classList").Call("remove", "open")
	}
}

func (a *App) closeSettingsDialog() {
	if a.settingsDialog.Truthy() {
		a.settingsDialog.Get("classList").Call("remove", "open")
	}
}

func (a *App) openDocumentDialog() {
	if !a.docDialog.Truthy() {
		return
	}
	a.docDlgName.Set("value", a.doc.Name)
	a.docDlgWidth.Set("value", fmt.Sprintf("%d", a.doc.Width))
	a.docDlgHeight.Set("value", fmt.Sprintf("%d", a.doc.Height))
	a.docDlgFps.Set("value", fmt.Sprintf("%d", a.doc.FPS))
	a.docDlgBg.Set("value", normalizeHexColor(a.doc.Background))
	a.docDialog.Get("classList").Call("add", "open")
	a.docDlgName.Call("focus")
}

func (a *App) openSettingsDialog() {
	if !a.settingsDialog.Truthy() {
		return
	}
	a.settingsMaxUndo.Set("value", fmt.Sprintf("%d", a.maxUndoChanges))
	a.settingsDialog.Get("classList").Call("add", "open")
	a.settingsMaxUndo.Call("focus")
}

func (a *App) submitDocumentDialog() {
	parseInt := func(input js.Value, min int) (int, bool) {
		v := int(input.Get("valueAsNumber").Float())
		if v >= min {
			return v, true
		}
		s := strings.TrimSpace(input.Get("value").String())
		n, err := strconv.Atoi(s)
		if err != nil || n < min {
			return 0, false
		}
		return n, true
	}

	name := strings.TrimSpace(a.docDlgName.Get("value").String())
	if name == "" {
		a.statusEl.Set("textContent", "Document name is required")
		return
	}
	width, ok := parseInt(a.docDlgWidth, 1)
	if !ok {
		a.statusEl.Set("textContent", "Document width must be at least 1")
		return
	}
	height, ok := parseInt(a.docDlgHeight, 1)
	if !ok {
		a.statusEl.Set("textContent", "Document height must be at least 1")
		return
	}
	fps, ok := parseInt(a.docDlgFps, 1)
	if !ok {
		a.statusEl.Set("textContent", "Document FPS must be at least 1")
		return
	}
	bg := strings.TrimSpace(a.docDlgBg.Get("value").String())
	if bg == "" {
		a.statusEl.Set("textContent", "Document background color is required")
		return
	}

	a.captureUndoSnapshot()
	a.doc.Name = name
	a.doc.Width = width
	a.doc.Height = height
	a.doc.FPS = fps
	a.doc.Background = normalizeHexColor(bg)
	a.refreshDocUI()
	a.resizeCanvases()
	a.closeDocumentDialog()
	a.statusEl.Set("textContent", "Document modified")
}

func (a *App) submitSettingsDialog() {
	parseInt := func(input js.Value, min int) (int, bool) {
		v := int(input.Get("valueAsNumber").Float())
		if v >= min {
			return v, true
		}
		s := strings.TrimSpace(input.Get("value").String())
		n, err := strconv.Atoi(s)
		if err != nil || n < min {
			return 0, false
		}
		return n, true
	}

	maxUndo, ok := parseInt(a.settingsMaxUndo, 1)
	if !ok {
		a.statusEl.Set("textContent", "Max Undo Changes must be greater than zero")
		return
	}
	a.maxUndoChanges = maxUndo
	a.trimUndoStack()
	a.closeSettingsDialog()
	a.statusEl.Set("textContent", "Application settings updated")
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
	layers := a.currentLayers()
	out := make([][2]int, 0, len(a.selectedInstances))
	for key := range a.selectedInstances {
		li, ii, ok := parseSelKey(key)
		if !ok {
			continue
		}
		if li < 0 || li >= len(layers) || ii < 0 || ii >= len(layers[li].Instances) {
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
	if a.selectedTweenLayerIdx != layerIdx || a.selectedTweenInstIdx != instIdx {
		a.clearTweenSelection()
	}
	if layerIdx >= 0 && instIdx >= 0 {
		a.activatePropertiesPanelForSelection()
	}
}

func (a *App) convertSelectedInstanceToSymbol() {
	layers := a.currentLayersPtr()
	if a.selectedLayerIdx < 0 || a.selectedLayerIdx >= len(*layers) || a.selectedInstIdx < 0 || a.selectedInstIdx >= len((*layers)[a.selectedLayerIdx].Instances) {
		a.statusEl.Set("textContent", "Select an instance to convert to a symbol")
		return
	}
	layer := &(*layers)[a.selectedLayerIdx]
	original := cloneInstance(layer.Instances[a.selectedInstIdx])
	if !isRenderableInstanceType(original) {
		a.statusEl.Set("textContent", "Only stage instances can be converted to a symbol")
		return
	}
	a.captureUndoSnapshot()

	symbolID := a.nextSymbolID()
	symbolName := fmt.Sprintf("Movie Clip %d", len(a.doc.Symbols)+1)
	nested := cloneInstance(original)
	nested.ID = symbolID + "-instance-1"
	symbol := Symbol{
		ID:         symbolID,
		Name:       symbolName,
		SymbolType: "movieclip",
		Layers: []Layer{{
			Name:        "Layer 1",
			Description: "MovieClip timeline layer",
			Color:       "#c77dff",
			Selected:    true,
			Instances:   []ElementInstance{nested},
		}},
		Instances: []ElementInstance{},
	}
	a.doc.Symbols = append(a.doc.Symbols, symbol)

	wrapper := ElementInstance{
		ID:          fmt.Sprintf("%s-wrapper", symbolID),
		Name:        "",
		Description: "Movie Clip instance",
		ElementType: "symbol",
		ElementID:   symbolID,
		Keyframes:   map[int]InstanceKeyframe{1: defaultKeyframeAt(1)},
	}
	if minX, minY, maxX, maxY, ok := a.symbolBoundsAtFrame(symbol, a.curFrame); ok {
		kf := wrapper.Keyframes[1]
		kf.AnchorX = (minX + maxX) / 2
		kf.AnchorY = (minY + maxY) / 2
		wrapper.Keyframes[1] = kf
	}
	layer.Instances[a.selectedInstIdx] = wrapper
	a.setSingleInstanceSelection(a.selectedLayerIdx, a.selectedInstIdx)
	a.updateSelectedLayerLabel()
	a.updateLibraryPanel()
	a.setRightPanelTab("library")
	a.statusEl.Set("textContent", "Converted selection to Movie Clip symbol")
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
			a.clearTweenSelection()
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

func (a *App) setSelectedTween(layerIdx, instIdx, startFrame, endFrame int) {
	a.selectedTweenLayerIdx = layerIdx
	a.selectedTweenInstIdx = instIdx
	a.selectedTweenStartFrame = startFrame
	a.selectedTweenEndFrame = endFrame
}

func (a *App) selectedTweenKeyframe() (InstanceKeyframe, bool) {
	if a.selectedTweenLayerIdx < 0 || a.selectedTweenInstIdx < 0 || a.selectedTweenStartFrame < 0 {
		return InstanceKeyframe{}, false
	}
	return a.getExactInstanceKeyframe(a.selectedTweenLayerIdx, a.selectedTweenInstIdx, a.selectedTweenStartFrame)
}

func (a *App) selectedInstanceTweenFrames(layerIdx, instIdx int) []int {
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) {
		return nil
	}
	if instIdx < 0 || instIdx >= len(layers[layerIdx].Instances) {
		return nil
	}
	inst := layers[layerIdx].Instances[instIdx]
	frames := make([]int, 0, len(inst.Keyframes))
	for frame := range inst.Keyframes {
		frames = append(frames, frame)
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			if frames[j] < frames[i] {
				frames[i], frames[j] = frames[j], frames[i]
			}
		}
	}
	return frames
}

func (a *App) pickTweenSpanAt(x, y float64) (int, int, int, int, bool) {
	if x <= a.headerW {
		return -1, -1, -1, -1, false
	}
	rowTop := 10.0 + 22.0 - 18.0
	layerIdx := int((y - rowTop) / a.layerH)
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) {
		return -1, -1, -1, -1, false
	}
	if a.selectedLayerIdx != layerIdx || a.selectedInstIdx < 0 {
		return -1, -1, -1, -1, false
	}
	instIdx := a.selectedInstIdx
	frames := a.selectedInstanceTweenFrames(layerIdx, instIdx)
	if len(frames) < 2 {
		return -1, -1, -1, -1, false
	}
	keyW := math.Max(6, a.zoom-4)
	for i := 0; i < len(frames)-1; i++ {
		start := frames[i]
		end := frames[i+1]
		x0 := a.frameToX(start) + 2 + keyW
		x1 := a.frameToX(end) + 2
		if x >= x0 && x <= x1 {
			return layerIdx, instIdx, start, end, true
		}
	}
	return -1, -1, -1, -1, false
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

func (a *App) findSymbolByID(id string) (Symbol, bool) {
	for _, s := range a.doc.Symbols {
		if s.ID == id {
			return s, true
		}
	}
	return Symbol{}, false
}

func (a *App) bitmapImageBySymbolID(id string) (js.Value, bool) {
	if a.bitmapImages == nil {
		return js.Value{}, false
	}
	img, ok := a.bitmapImages[id]
	if !ok || !img.Truthy() {
		return js.Value{}, false
	}
	return img, true
}

func cloneKeyframes(src map[int]InstanceKeyframe) map[int]InstanceKeyframe {
	dst := make(map[int]InstanceKeyframe, len(src))
	for frame, kf := range src {
		dst[frame] = kf
	}
	return dst
}

func cloneInstance(inst ElementInstance) ElementInstance {
	copy := inst
	copy.Keyframes = cloneKeyframes(inst.Keyframes)
	return copy
}

func dist(x1, y1, x2, y2 float64) float64 { return math.Hypot(x1-x2, y1-y2) }

func bitmapSource(sym Symbol) string {
	if strings.TrimSpace(sym.BitmapData) != "" {
		return sym.BitmapData
	}
	return strings.TrimSpace(sym.AssetURL)
}

func (a *App) syncBitmapAssets() {
	if a.bitmapImages == nil {
		a.bitmapImages = make(map[string]js.Value)
	}
	next := make(map[string]js.Value)
	for _, sym := range a.doc.Symbols {
		src := bitmapSource(sym)
		if sym.SymbolType != "bitmap" || src == "" {
			continue
		}
		if img, ok := a.bitmapImages[sym.ID]; ok && img.Truthy() && img.Get("src").String() == src {
			next[sym.ID] = img
			continue
		}
		img := js.Global().Get("Image").New()
		loadCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			a.renderAll()
			return nil
		})
		errorCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			a.renderAll()
			return nil
		})
		a.holdCallback(loadCb)
		a.holdCallback(errorCb)
		img.Call("addEventListener", "load", loadCb)
		img.Call("addEventListener", "error", errorCb)
		img.Set("src", src)
		next[sym.ID] = img
	}
	a.bitmapImages = next
}

func (a *App) currentLayersPtr() *[]Layer {
	if len(a.timelinePath) == 0 {
		return &a.doc.Layers
	}
	symbolID := a.timelinePath[len(a.timelinePath)-1]
	for i := range a.doc.Symbols {
		if a.doc.Symbols[i].ID == symbolID {
			return &a.doc.Symbols[i].Layers
		}
	}
	return &a.doc.Layers
}

func (a *App) currentLayers() []Layer {
	return *a.currentLayersPtr()
}

func (a *App) currentTimelineBreadcrumb() string {
	parts := []string{"Root"}
	for _, symbolID := range a.timelinePath {
		parts = append(parts, a.symbolNameByID(symbolID))
	}
	return strings.Join(parts, " > ")
}

func (a *App) enterMovieClipTimeline(symbolID string) bool {
	sym, ok := a.findSymbolByID(symbolID)
	if !ok || sym.SymbolType != "movieclip" {
		return false
	}
	a.timelinePath = append(a.timelinePath, symbolID)
	a.clearInstanceSelection()
	layers := a.currentLayersPtr()
	if len(*layers) == 0 {
		*layers = []Layer{{
			Name:        "Layer 1",
			Description: "Default empty layer",
			Color:       "#c77dff",
			Selected:    true,
			Instances:   []ElementInstance{},
		}}
	} else {
		hasSelected := false
		for _, layer := range *layers {
			if layer.Selected {
				hasSelected = true
				break
			}
		}
		if !hasSelected {
			(*layers)[0].Selected = true
		}
	}
	a.refreshDocUI()
	a.updateSelectedLayerLabel()
	a.statusEl.Set("textContent", "Entered "+sym.Name)
	return true
}

func (a *App) exitMovieClipTimeline() bool {
	if len(a.timelinePath) == 0 {
		return false
	}
	a.timelinePath = a.timelinePath[:len(a.timelinePath)-1]
	a.clearInstanceSelection()
	a.refreshDocUI()
	a.updateSelectedLayerLabel()
	a.statusEl.Set("textContent", "Returned to "+a.currentTimelineBreadcrumb())
	return true
}

func normalizeHexColor(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "")
	if len(s) == 3 && !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if len(s) == 6 && !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
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

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func alphaFromColor(s string) float64 {
	s = strings.TrimSpace(strings.ToLower(strings.ReplaceAll(s, " ", "")))
	if strings.HasPrefix(s, "rgba(") {
		var r, g, b int
		var a float64
		if _, err := fmt.Sscanf(s, "rgba(%d,%d,%d,%f)", &r, &g, &b, &a); err == nil {
			return clamp01(a)
		}
	}
	return 1
}

func colorWithAlpha(color string, alpha float64) string {
	hex := normalizeHexColor(color)
	alpha = clamp01(alpha)
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return hex
	}
	if alpha >= 0.999 {
		return hex
	}
	return fmt.Sprintf("rgba(%d, %d, %d, %.3f)", r, g, b, alpha)
}

func setColorPickerButtonAppearance(el js.Value, color string) {
	if !el.Truthy() {
		return
	}
	hex := normalizeHexColor(color)
	alpha := alphaFromColor(color)
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err == nil {
		brightness := (0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)) / 255.0
		if brightness > 0.5 {
			el.Get("style").Set("color", "#000000")
		} else {
			el.Get("style").Set("color", "#ffffff")
		}
	}
	el.Set("textContent", fmt.Sprintf("%s  %d%%", strings.ToUpper(hex), int(math.Round(alpha*100))))
	el.Get("style").Set("background", colorWithAlpha(hex, alpha))
}

func setColorPreviewAppearance(el js.Value, color string) {
	if !el.Truthy() {
		return
	}
	hex := normalizeHexColor(color)
	alpha := alphaFromColor(color)
	el.Set("textContent", "")
	el.Get("style").Set("background", colorWithAlpha(hex, alpha))
}

func (a *App) closeColorPicker() {
	if a.colorPickerPopover.Truthy() {
		a.colorPickerPopover.Get("classList").Call("remove", "open")
	}
	a.activeColorField = ""
	a.activeColorBtn = js.Null()
	a.colorPickerDirty = false
}

func (a *App) openColorPicker(field string, btn js.Value, color string) {
	if !btn.Truthy() || !a.colorPickerPopover.Truthy() {
		return
	}
	a.activeColorField = field
	a.activeColorBtn = btn
	a.colorPickerDirty = false
	hex := normalizeHexColor(color)
	alpha := alphaFromColor(color)
	a.colorPickerColor.Set("value", hex)
	a.colorPickerAlpha.Set("value", fmt.Sprintf("%.2f", alpha))
	a.colorPickerAlphaValue.Set("value", fmt.Sprintf("%.2f", alpha))
	a.syncColorPickerPreview()
	rect := btn.Call("getBoundingClientRect")
	left := rect.Get("left").Float()
	top := rect.Get("bottom").Float() + 6
	a.colorPickerPopover.Get("style").Set("left", fmt.Sprintf("%.0fpx", left))
	a.colorPickerPopover.Get("style").Set("top", fmt.Sprintf("%.0fpx", top))
	a.colorPickerPopover.Get("classList").Call("add", "open")
}

func (a *App) syncColorPickerPreview() {
	if !a.colorPickerPreview.Truthy() {
		return
	}
	color := a.currentPickerColor()
	setColorPreviewAppearance(a.colorPickerPreview, color)
	if a.colorPickerPreviewText.Truthy() {
		hex := normalizeHexColor(color)
		a.colorPickerPreviewText.Set("value", strings.ToUpper(hex))
	}
}

func (a *App) applyColorPickerHexInput(raw string) {
	hex := normalizeHexColor(raw)
	a.colorPickerColor.Set("value", hex)
	a.applyActiveColorPicker()
}

func (a *App) currentPickerColor() string {
	alpha, err := strconv.ParseFloat(strings.TrimSpace(a.colorPickerAlphaValue.Get("value").String()), 64)
	if err != nil {
		alpha = a.colorPickerAlpha.Get("valueAsNumber").Float()
	}
	return colorWithAlpha(a.colorPickerColor.Get("value").String(), alpha)
}

func (a *App) applyActiveColorPicker() {
	if a.activeColorField == "" {
		return
	}
	alpha, err := strconv.ParseFloat(strings.TrimSpace(a.colorPickerAlphaValue.Get("value").String()), 64)
	if err != nil {
		alpha = a.colorPickerAlpha.Get("valueAsNumber").Float()
	}
	alpha = clamp01(alpha)
	a.colorPickerAlpha.Set("value", fmt.Sprintf("%.2f", alpha))
	a.colorPickerAlphaValue.Set("value", fmt.Sprintf("%.2f", alpha))
	switch a.activeColorField {
	case "docBg":
		a.captureUndoSnapshot()
		a.doc.Background = colorWithAlpha(a.colorPickerColor.Get("value").String(), alpha)
		a.refreshDocUI()
	case "toolFill":
		a.shapeToolFill = colorWithAlpha(a.colorPickerColor.Get("value").String(), alpha)
	case "toolStroke":
		a.shapeToolStroke = colorWithAlpha(a.colorPickerColor.Get("value").String(), alpha)
	default:
		a.applyShapePaint(a.activeColorField, a.colorPickerColor.Get("value").String(), alpha)
	}
	a.colorPickerDirty = true
	a.syncColorPickerPreview()
}

func (a *App) applyTransformField(field string, value float64) {
	a.captureUndoSnapshot()
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		kf, ok := a.writableTransformKeyframe(li, ii, a.curFrame)
		if !ok {
			continue
		}
		switch field {
		case "x":
			kf.X = value
		case "y":
			kf.Y = value
		case "scaleX":
			if a.lockScale {
				if math.Abs(kf.ScaleX) > 1e-6 {
					kf.ScaleY *= value / kf.ScaleX
				} else {
					kf.ScaleY = value
				}
			}
			kf.ScaleX = value
		case "scaleY":
			if a.lockScale {
				if math.Abs(kf.ScaleY) > 1e-6 {
					kf.ScaleX *= value / kf.ScaleY
				} else {
					kf.ScaleX = value
				}
			}
			kf.ScaleY = value
		case "skewX":
			kf.SkewX = value
		case "skewY":
			kf.SkewY = value
		case "rotation":
			kf.Rotation = value * math.Pi / 180
		case "anchorX":
			kf.AnchorX = value
		case "anchorY":
			kf.AnchorY = value
		}
		a.setInstanceKeyframe(li, ii, a.curFrame, kf)
	}
}

func (a *App) updateScaleLockUI() {
	if !a.propScaleLock.Truthy() {
		return
	}
	if a.lockScale {
		a.propScaleLock.Set("textContent", "⛓")
		a.propScaleLock.Set("title", "Lock Scale: On")
		a.propScaleLock.Get("classList").Call("add", "active")
		return
	}
	a.propScaleLock.Set("textContent", "⛓×")
	a.propScaleLock.Set("title", "Lock Scale: Off")
	a.propScaleLock.Get("classList").Call("remove", "active")
}

func (a *App) applyInstanceName(value string) {
	layers := a.currentLayersPtr()
	if a.selectedLayerIdx < 0 || a.selectedLayerIdx >= len(*layers) || a.selectedInstIdx < 0 || a.selectedInstIdx >= len((*layers)[a.selectedLayerIdx].Instances) {
		return
	}
	a.captureUndoSnapshot()
	(*layers)[a.selectedLayerIdx].Instances[a.selectedInstIdx].Name = value
	a.updateSelectedLayerLabel()
	a.updatePropertiesPanel()
}

func (a *App) applyShapePaint(field, color string, alpha float64) {
	base := normalizeHexColor(color)
	alpha = clamp01(alpha)
	a.captureUndoSnapshot()
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.currentLayers()[li].Instances[ii]
		switch inst.ElementType {
		case "path":
			for pi := range a.doc.Paths {
				if a.doc.Paths[pi].ID != inst.ElementID {
					continue
				}
				if field == "fill" {
					a.doc.Paths[pi].Fill = colorWithAlpha(base, alpha)
				} else {
					a.doc.Paths[pi].Stroke = colorWithAlpha(base, alpha)
				}
				break
			}
		case "circle":
			for ci := range a.doc.Circles {
				if a.doc.Circles[ci].ID != inst.ElementID {
					continue
				}
				if field == "fill" {
					a.doc.Circles[ci].Fill = colorWithAlpha(base, alpha)
				} else {
					a.doc.Circles[ci].Stroke = colorWithAlpha(base, alpha)
				}
				break
			}
		}
	}
}

func (a *App) applyShapeColor(field, color string) {
	alpha := 1.0
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.currentLayers()[li].Instances[ii]
		if inst.ElementType == "path" {
			if p, ok := a.findPathByID(inst.ElementID); ok {
				if field == "fill" {
					alpha = alphaFromColor(p.Fill)
				} else {
					alpha = alphaFromColor(p.Stroke)
				}
				break
			}
		}
		if inst.ElementType == "circle" {
			if c, ok := a.findCircleByID(inst.ElementID); ok {
				if field == "fill" {
					alpha = alphaFromColor(c.Fill)
				} else {
					alpha = alphaFromColor(c.Stroke)
				}
				break
			}
		}
	}
	a.applyShapePaint(field, color, alpha)
}

func (a *App) applyShapeAlpha(field string, alpha float64) {
	base := "#66e3ff"
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.currentLayers()[li].Instances[ii]
		if inst.ElementType == "path" {
			if p, ok := a.findPathByID(inst.ElementID); ok {
				if field == "fill" {
					base = normalizeHexColor(p.Fill)
				} else {
					base = normalizeHexColor(p.Stroke)
				}
				break
			}
		}
		if inst.ElementType == "circle" {
			if c, ok := a.findCircleByID(inst.ElementID); ok {
				if field == "fill" {
					base = normalizeHexColor(c.Fill)
				} else {
					base = normalizeHexColor(c.Stroke)
				}
				break
			}
		}
	}
	a.applyShapePaint(field, base, alpha)
}

func (a *App) applyShapeNumeric(field string, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	if field == "fillAlpha" {
		a.applyShapeAlpha("fill", value)
		return
	}
	if field == "strokeAlpha" {
		a.applyShapeAlpha("stroke", value)
		return
	}
	if field == "strokeW" && value < 0 {
		value = 0
	}
	a.captureUndoSnapshot()
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		inst := a.currentLayers()[li].Instances[ii]
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
	a.captureUndoSnapshot()
	for _, pair := range a.selectedInstancePairsOrPrimary() {
		li, ii := pair[0], pair[1]
		kf, ok := a.writableTransformKeyframe(li, ii, a.curFrame)
		if !ok {
			continue
		}
		kf.Rotation += deltaRad
		a.setInstanceKeyframe(li, ii, a.curFrame, kf)
	}
}

func (a *App) applySelectedTweenEase(mode, dir string) {
	kf, ok := a.selectedTweenKeyframe()
	if !ok {
		return
	}
	a.captureUndoSnapshot()
	kf.EaseMode = normalizeEaseMode(mode)
	kf.EaseDir = normalizeEaseDir(dir)
	a.setInstanceKeyframe(a.selectedTweenLayerIdx, a.selectedTweenInstIdx, a.selectedTweenStartFrame, kf)
}

func (a *App) applyDocumentField(field string, value string) {
	value = strings.TrimSpace(value)
	switch field {
	case "name":
		if value == "" || value == a.doc.Name {
			return
		}
		a.captureUndoSnapshot()
		a.doc.Name = value
		a.refreshDocUI()
	case "width":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n == a.doc.Width {
			return
		}
		a.captureUndoSnapshot()
		a.doc.Width = n
		a.refreshDocUI()
		a.resizeCanvases()
	case "height":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n == a.doc.Height {
			return
		}
		a.captureUndoSnapshot()
		a.doc.Height = n
		a.refreshDocUI()
		a.resizeCanvases()
	case "fps":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || n == a.doc.FPS {
			return
		}
		a.captureUndoSnapshot()
		a.doc.FPS = n
		a.refreshDocUI()
	}
}

func (a *App) addKeyframeForSelectedInstances() {
	pairs := a.selectedInstancePairsOrPrimary()
	if len(pairs) == 0 {
		a.statusEl.Set("textContent", "Select an instance to add a keyframe")
		return
	}

	added := 0
	captured := false
	for _, pair := range pairs {
		li, ii := pair[0], pair[1]
		if _, exists := a.getExactInstanceKeyframe(li, ii, a.curFrame); exists {
			continue
		}
		if !captured {
			a.captureUndoSnapshot()
			captured = true
		}
		a.addKeyframe(li, ii, a.curFrame)
		added++
	}

	switch {
	case added == 0:
		a.statusEl.Set("textContent", fmt.Sprintf("Selected instance already has a keyframe at %d", a.curFrame))
	case added == 1:
		a.statusEl.Set("textContent", fmt.Sprintf("Keyframe added at %d", a.curFrame))
	default:
		a.statusEl.Set("textContent", fmt.Sprintf("%d keyframes added at %d", added, a.curFrame))
	}
}

func (a *App) updatePropertiesPanel() {
	hasSel := a.selectedLayerIdx >= 0 && a.selectedInstIdx >= 0
	layers := a.currentLayers()
	if hasSel && (a.selectedLayerIdx < 0 || a.selectedLayerIdx >= len(layers) || a.selectedInstIdx < 0 || a.selectedInstIdx >= len(layers[a.selectedLayerIdx].Instances)) {
		hasSel = false
	}
	d := js.Global().Get("document")
	setDisplay := func(id, display string) {
		el := d.Call("getElementById", id)
		if el.Truthy() {
			el.Get("style").Set("display", display)
		}
	}
	if hasSel {
		setDisplay("docPropertiesSection", "none")
		setDisplay("objectPropertiesSection", "block")
	} else {
		setDisplay("docPropertiesSection", "block")
		setDisplay("objectPropertiesSection", "none")
	}
	if a.propName.Truthy() {
		a.propName.Set("disabled", !hasSel)
	}
	docControls := []js.Value{a.propDocName, a.propDocWidth, a.propDocHeight, a.propDocFps, a.propDocBg}
	for _, c := range docControls {
		if c.Truthy() {
			c.Set("disabled", hasSel)
		}
	}
	if a.propDocName.Truthy() {
		setInputValueIfUnfocused(a.propDocName, a.doc.Name)
	}
	if a.propDocWidth.Truthy() {
		setInputValueIfUnfocused(a.propDocWidth, fmt.Sprintf("%d", a.doc.Width))
	}
	if a.propDocHeight.Truthy() {
		setInputValueIfUnfocused(a.propDocHeight, fmt.Sprintf("%d", a.doc.Height))
	}
	if a.propDocFps.Truthy() {
		setInputValueIfUnfocused(a.propDocFps, fmt.Sprintf("%d", a.doc.FPS))
	}
	if a.propDocBg.Truthy() {
		setColorPickerButtonAppearance(a.propDocBg, a.doc.Background)
	}
	transformControls := []js.Value{a.propPosX, a.propPosY, a.propScaleX, a.propScaleY, a.propScaleLock, a.propSkewX, a.propSkewY, a.propRot, a.propRotDec, a.propRotInc, a.propAncX, a.propAncY}
	shapeControls := []js.Value{a.propFill, a.propStroke, a.propStrokeW, a.toolFill, a.toolStroke}
	tweenControls := []js.Value{a.propEaseMode, a.propEaseDir}
	for _, c := range append(append(transformControls, shapeControls...), tweenControls...) {
		if !c.Truthy() {
			continue
		}
		c.Set("disabled", !hasSel)
	}
	if !hasSel {
		if a.propName.Truthy() {
			setInputValueIfUnfocused(a.propName, "")
		}
		a.updateScaleLockUI()
		if a.toolFill.Truthy() {
			a.toolFill.Set("disabled", false)
		}
		if a.toolStroke.Truthy() {
			a.toolStroke.Set("disabled", false)
		}
		setColorPickerButtonAppearance(a.toolFill, a.shapeToolFill)
		setColorPickerButtonAppearance(a.toolStroke, a.shapeToolStroke)
		return
	}

	inst := layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
	a.updateScaleLockUI()
	if a.propName.Truthy() {
		setInputValueIfUnfocused(a.propName, inst.Name)
	}
	kf, exact := a.getExactInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame)
	for _, c := range transformControls {
		if c.Truthy() {
			c.Set("disabled", !exact && !a.autoKey)
		}
	}
	if exact {
		setInputValueIfUnfocused(a.propPosX, fmt.Sprintf("%.2f", kf.X))
		setInputValueIfUnfocused(a.propPosY, fmt.Sprintf("%.2f", kf.Y))
		setInputValueIfUnfocused(a.propScaleX, fmt.Sprintf("%.3f", kf.ScaleX))
		setInputValueIfUnfocused(a.propScaleY, fmt.Sprintf("%.3f", kf.ScaleY))
		setInputValueIfUnfocused(a.propSkewX, fmt.Sprintf("%.3f", kf.SkewX))
		setInputValueIfUnfocused(a.propSkewY, fmt.Sprintf("%.3f", kf.SkewY))
		setInputValueIfUnfocused(a.propRot, fmt.Sprintf("%.3f", kf.Rotation*180/math.Pi))
		setInputValueIfUnfocused(a.propAncX, fmt.Sprintf("%.2f", kf.AnchorX))
		setInputValueIfUnfocused(a.propAncY, fmt.Sprintf("%.2f", kf.AnchorY))
	} else if kf, ok := a.getInstanceKeyframe(a.selectedLayerIdx, a.selectedInstIdx, a.curFrame); ok {
		setInputValueIfUnfocused(a.propPosX, fmt.Sprintf("%.2f", kf.X))
		setInputValueIfUnfocused(a.propPosY, fmt.Sprintf("%.2f", kf.Y))
		setInputValueIfUnfocused(a.propScaleX, fmt.Sprintf("%.3f", kf.ScaleX))
		setInputValueIfUnfocused(a.propScaleY, fmt.Sprintf("%.3f", kf.ScaleY))
		setInputValueIfUnfocused(a.propSkewX, fmt.Sprintf("%.3f", kf.SkewX))
		setInputValueIfUnfocused(a.propSkewY, fmt.Sprintf("%.3f", kf.SkewY))
		setInputValueIfUnfocused(a.propRot, fmt.Sprintf("%.3f", kf.Rotation*180/math.Pi))
		setInputValueIfUnfocused(a.propAncX, fmt.Sprintf("%.2f", kf.AnchorX))
		setInputValueIfUnfocused(a.propAncY, fmt.Sprintf("%.2f", kf.AnchorY))
	}
	shape := inst.ElementType == "path" || inst.ElementType == "circle"
	a.propFill.Set("disabled", !shape)
	a.propStroke.Set("disabled", !shape)
	a.propStrokeW.Set("disabled", !shape)
	if row := a.propFill.Get("parentElement").Get("parentElement"); row.Truthy() {
		if shape {
			row.Get("style").Set("display", "")
		} else {
			row.Get("style").Set("display", "none")
		}
	}
	if row := a.propStroke.Get("parentElement").Get("parentElement"); row.Truthy() {
		if shape {
			row.Get("style").Set("display", "")
		} else {
			row.Get("style").Set("display", "none")
		}
	}
	if row := a.propStrokeW.Get("parentElement").Get("parentElement"); row.Truthy() {
		if shape {
			row.Get("style").Set("display", "")
		} else {
			row.Get("style").Set("display", "none")
		}
	}
	if a.toolFill.Truthy() {
		a.toolFill.Set("disabled", false)
	}
	if a.toolStroke.Truthy() {
		a.toolStroke.Set("disabled", false)
	}
	setColorPickerButtonAppearance(a.toolFill, a.shapeToolFill)
	setColorPickerButtonAppearance(a.toolStroke, a.shapeToolStroke)
	if inst.ElementType == "path" {
		if p, ok := a.findPathByID(inst.ElementID); ok {
			setColorPickerButtonAppearance(a.propFill, p.Fill)
			setColorPickerButtonAppearance(a.propStroke, p.Stroke)
			setInputValueIfUnfocused(a.propStrokeW, fmt.Sprintf("%.2f", p.StrokeW))
		}
	}
	if inst.ElementType == "circle" {
		if c, ok := a.findCircleByID(inst.ElementID); ok {
			setColorPickerButtonAppearance(a.propFill, c.Fill)
			setColorPickerButtonAppearance(a.propStroke, c.Stroke)
			setInputValueIfUnfocused(a.propStrokeW, fmt.Sprintf("%.2f", c.StrokeW))
		}
	}
	hasTween := a.selectedTweenLayerIdx == a.selectedLayerIdx &&
		a.selectedTweenInstIdx == a.selectedInstIdx &&
		a.selectedTweenStartFrame >= 0 &&
		a.selectedTweenEndFrame > a.selectedTweenStartFrame
	a.propEaseMode.Set("disabled", !hasTween)
	a.propEaseDir.Set("disabled", !hasTween)
	if hasTween {
		if tweenKF, ok := a.selectedTweenKeyframe(); ok {
			setInputValueIfUnfocused(a.propEaseMode, normalizeEaseMode(tweenKF.EaseMode))
			setInputValueIfUnfocused(a.propEaseDir, normalizeEaseDir(tweenKF.EaseDir))
		}
	}
}

func setInputValueIfUnfocused(el js.Value, value string) {
	if !el.Truthy() {
		return
	}
	d := js.Global().Get("document")
	if d.Truthy() {
		active := d.Get("activeElement")
		if active.Truthy() && active.Equal(el) {
			return
		}
	}
	el.Set("value", value)
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

func unionBounds(minX, minY, maxX, maxY float64, ok bool, bx0, by0, bx1, by1 float64) (float64, float64, float64, float64, bool) {
	if !ok {
		return bx0, by0, bx1, by1, true
	}
	if bx0 < minX {
		minX = bx0
	}
	if by0 < minY {
		minY = by0
	}
	if bx1 > maxX {
		maxX = bx1
	}
	if by1 > maxY {
		maxY = by1
	}
	return minX, minY, maxX, maxY, true
}

func transformBounds(m mat2d, minX, minY, maxX, maxY float64) (float64, float64, float64, float64) {
	corners := [][2]float64{{minX, minY}, {maxX, minY}, {maxX, maxY}, {minX, maxY}}
	x0, y0 := matApply(m, corners[0][0], corners[0][1])
	outMinX, outMinY, outMaxX, outMaxY := x0, y0, x0, y0
	for i := 1; i < len(corners); i++ {
		x, y := matApply(m, corners[i][0], corners[i][1])
		if x < outMinX {
			outMinX = x
		}
		if y < outMinY {
			outMinY = y
		}
		if x > outMaxX {
			outMaxX = x
		}
		if y > outMaxY {
			outMaxY = y
		}
	}
	return outMinX, outMinY, outMaxX, outMaxY
}

func (a *App) symbolBoundsAtFrame(sym Symbol, frame int) (float64, float64, float64, float64, bool) {
	if sym.SymbolType == "bitmap" && sym.BitmapW > 0 && sym.BitmapH > 0 {
		return 0, 0, sym.BitmapW, sym.BitmapH, true
	}
	var minX, minY, maxX, maxY float64
	have := false
	for _, layer := range sym.Layers {
		for _, nested := range layer.Instances {
			bx0, by0, bx1, by1, ok := a.instanceBoundsRecursive(nested, frame, 0)
			if !ok {
				continue
			}
			minX, minY, maxX, maxY, have = unionBounds(minX, minY, maxX, maxY, have, bx0, by0, bx1, by1)
		}
	}
	if !have {
		for _, nested := range sym.Instances {
			bx0, by0, bx1, by1, ok := a.instanceBoundsRecursive(nested, frame, 0)
			if !ok {
				continue
			}
			minX, minY, maxX, maxY, have = unionBounds(minX, minY, maxX, maxY, have, bx0, by0, bx1, by1)
		}
	}
	return minX, minY, maxX, maxY, have
}

func (a *App) instanceBoundsRecursive(inst ElementInstance, frame, depth int) (float64, float64, float64, float64, bool) {
	if depth > 8 {
		return 0, 0, 0, 0, false
	}
	kf, ok := resolveElementInstanceKeyframe(inst, frame, a.doc.TotalFrames)
	if !ok {
		return 0, 0, 0, 0, false
	}
	m := instanceMatrix(kf)
	switch inst.ElementType {
	case "path":
		p, ok := a.findPathByID(inst.ElementID)
		if !ok {
			return 0, 0, 0, 0, false
		}
		minX, minY, maxX, maxY, ok := pathLocalBounds(p)
		if !ok {
			return 0, 0, 0, 0, false
		}
		tx0, ty0, tx1, ty1 := transformBounds(m, minX, minY, maxX, maxY)
		return tx0, ty0, tx1, ty1, true
	case "circle":
		c, ok := a.findCircleByID(inst.ElementID)
		if !ok {
			return 0, 0, 0, 0, false
		}
		minX, minY, maxX, maxY := transformBounds(m, c.CX-c.Radius, c.CY-c.Radius, c.CX+c.Radius, c.CY+c.Radius)
		return minX, minY, maxX, maxY, true
	case "symbol":
		sym, ok := a.findSymbolByID(inst.ElementID)
		if !ok {
			return 0, 0, 0, 0, false
		}
		minX, minY, maxX, maxY, ok := a.symbolBoundsAtFrame(sym, frame)
		if !ok {
			return 0, 0, 0, 0, false
		}
		tx0, ty0, tx1, ty1 := transformBounds(m, minX, minY, maxX, maxY)
		return tx0, ty0, tx1, ty1, true
	}
	return 0, 0, 0, 0, false
}

func (a *App) drawInstanceRecursive(ctx js.Value, inst ElementInstance, frame, depth int) {
	if depth > 8 || !isRenderableInstanceType(inst) {
		return
	}
	kf, ok := resolveElementInstanceKeyframe(inst, frame, a.doc.TotalFrames)
	if !ok {
		return
	}
	ctx.Call("save")
	m := instanceMatrix(kf)
	ctx.Call("transform", m.a, m.b, m.c, m.d, m.e, m.f)
	switch inst.ElementType {
	case "path":
		if p, ok := a.findPathByID(inst.ElementID); ok {
			drawPathLocal(ctx, p)
		}
	case "circle":
		if c, ok := a.findCircleByID(inst.ElementID); ok {
			drawCircleLocal(ctx, c)
		}
	case "symbol":
		if sym, ok := a.findSymbolByID(inst.ElementID); ok {
			if sym.SymbolType == "bitmap" {
				if img, ok := a.bitmapImageBySymbolID(sym.ID); ok {
					ctx.Call("drawImage", img, 0, 0, sym.BitmapW, sym.BitmapH)
				}
			} else {
				for _, layer := range sym.Layers {
					for _, nested := range layer.Instances {
						a.drawInstanceRecursive(ctx, nested, frame, depth+1)
					}
				}
				if len(sym.Layers) == 0 {
					for _, nested := range sym.Instances {
						a.drawInstanceRecursive(ctx, nested, frame, depth+1)
					}
				}
			}
		}
	}
	ctx.Call("restore")
}

func (a *App) instanceBoundsWorld(layerIdx, instIdx int) (float64, float64, float64, float64, bool) {
	layers := a.currentLayers()
	if layerIdx < 0 || layerIdx >= len(layers) || instIdx < 0 || instIdx >= len(layers[layerIdx].Instances) {
		return 0, 0, 0, 0, false
	}
	inst := layers[layerIdx].Instances[instIdx]
	if !isRenderableInstanceType(inst) {
		return 0, 0, 0, 0, false
	}
	return a.instanceBoundsRecursive(inst, a.curFrame, 0)
}

func (a *App) pickInstanceAt(x, y float64) (int, int, bool) {
	layers := a.currentLayers()
	for li := len(layers) - 1; li >= 0; li-- {
		layer := layers[li]
		for ii := len(layer.Instances) - 1; ii >= 0; ii-- {
			inst := layer.Instances[ii]
			if !isRenderableInstanceType(inst) {
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
	a.captureUndoSnapshot()

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
	a.timelinePath = nil
	a.syncBitmapAssets()
	a.undoStack = nil
	a.redoStack = nil
	a.clearInstanceSelection()
	a.setFrame(a.curFrame)
	a.refreshDocUI()
	a.renderAll()
	return nil
}

func (a *App) saveDocumentToDisk() error {
	a.syncBitmapAssets()
	data, err := json.MarshalIndent(a.doc, "", "  ")
	if err != nil {
		return err
	}

	u8 := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(u8, data)

	blob := js.Global().Get("Blob").New([]any{u8}, map[string]any{
		"type": "application/json",
	})
	w := js.Global().Get("window")
	if w.Truthy() && !w.Get("showSaveFilePicker").IsUndefined() {
		opts := map[string]any{
			"suggestedName": sanitizeFileName(a.doc.Name) + ".json",
			"types": []any{
				map[string]any{
					"description": "JSON Documents",
					"accept": map[string]any{
						"application/json": []any{".json"},
					},
				},
			},
		}
		pickerPromise := w.Call("showSaveFilePicker", opts)
		saveThenCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			handle := args[0]
			writeThenCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				if len(args) == 0 {
					return nil
				}
				writable := args[0]
				writeDoneCb := js.FuncOf(func(this js.Value, args []js.Value) any {
					closeThenCb := js.FuncOf(func(this js.Value, args []js.Value) any {
						a.statusEl.Set("textContent", "Document saved")
						return nil
					})
					closeCatchCb := js.FuncOf(func(this js.Value, args []js.Value) any {
						msg := "Unknown error"
						if len(args) > 0 {
							msg = args[0].String()
						}
						a.statusEl.Set("textContent", "Save failed: "+msg)
						return nil
					})
					a.holdCallback(closeThenCb)
					a.holdCallback(closeCatchCb)
					writable.Call("close").Call("then", closeThenCb).Call("catch", closeCatchCb)
					return nil
				})
				writeOpCatchCb := js.FuncOf(func(this js.Value, args []js.Value) any {
					msg := "Unknown error"
					if len(args) > 0 {
						msg = args[0].String()
					}
					a.statusEl.Set("textContent", "Save failed: "+msg)
					return nil
				})
				a.holdCallback(writeDoneCb)
				a.holdCallback(writeOpCatchCb)
				writable.Call("write", blob).Call("then", writeDoneCb).Call("catch", writeOpCatchCb)
				return nil
			})
			writeCatchCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				msg := "Unknown error"
				if len(args) > 0 {
					msg = args[0].String()
				}
				a.statusEl.Set("textContent", "Save failed: "+msg)
				return nil
			})
			a.holdCallback(writeThenCb)
			a.holdCallback(writeCatchCb)
			handle.Call("createWritable").Call("then", writeThenCb).Call("catch", writeCatchCb)
			return nil
		})
		saveCatchCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			msg := "Save cancelled"
			if len(args) > 0 {
				name := args[0].Get("name").String()
				if name != "AbortError" && args[0].String() != "" {
					msg = "Save failed: " + args[0].String()
				}
			}
			a.statusEl.Set("textContent", msg)
			return nil
		})
		a.holdCallback(saveThenCb)
		a.holdCallback(saveCatchCb)
		pickerPromise.Call("then", saveThenCb).Call("catch", saveCatchCb)
		a.statusEl.Set("textContent", "Choose where to save the document")
		return nil
	}

	url := js.Global().Get("URL").Call("createObjectURL", blob)
	d := js.Global().Get("document")
	aEl := d.Call("createElement", "a")
	aEl.Set("href", url)
	aEl.Set("download", sanitizeFileName(a.doc.Name)+".json")
	d.Get("body").Call("appendChild", aEl)
	aEl.Call("click")
	d.Get("body").Call("removeChild", aEl)
	js.Global().Get("URL").Call("revokeObjectURL", url)
	a.statusEl.Set("textContent", "Document downloaded as JSON")
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

func (a *App) importBitmapSymbol(name, bitmapData string, width, height float64) {
	if width <= 0 || height <= 0 {
		a.statusEl.Set("textContent", "Import failed: invalid image size")
		return
	}
	symbolID := a.nextSymbolID()
	symbolName := baseNameWithoutExt(name)
	a.captureUndoSnapshot()
	symbol := Symbol{
		ID:         symbolID,
		Name:       symbolName,
		SymbolType: "bitmap",
		BitmapData: bitmapData,
		BitmapW:    width,
		BitmapH:    height,
		Instances:  []ElementInstance{},
	}
	a.doc.Symbols = append(a.doc.Symbols, symbol)
	a.syncBitmapAssets()

	layerIdx := a.targetLayerForImportedBitmap(symbolName)
	if layerIdx < 0 || layerIdx >= len(a.doc.Layers) {
		return
	}
	layer := &a.doc.Layers[layerIdx]
	kf := defaultKeyframeAt(a.curFrame)
	kf.AnchorX = width / 2
	kf.AnchorY = height / 2
	kf.X = float64(a.doc.Width)/2 - kf.AnchorX
	kf.Y = float64(a.doc.Height)/2 - kf.AnchorY
	inst := ElementInstance{
		ID:          fmt.Sprintf("%s-instance-%d", symbolID, len(layer.Instances)+1),
		Name:        "",
		Description: "Bitmap instance",
		ElementType: "symbol",
		ElementID:   symbolID,
		Keyframes:   map[int]InstanceKeyframe{a.curFrame: kf},
	}
	layer.Instances = append([]ElementInstance{inst}, layer.Instances...)
	a.setSingleInstanceSelection(layerIdx, 0)
	a.selectedLibrarySymbolID = symbolID
	a.updateSelectedLayerLabel()
	a.updateLibraryPanel()
	a.setRightPanelTab("library")
	a.renderAll()
	a.statusEl.Set("textContent", fmt.Sprintf("Imported image %s", symbolName))
}

func (a *App) importImageFromDisk() error {
	d := js.Global().Get("document")
	input := d.Call("createElement", "input")
	input.Set("type", "file")
	input.Set("accept", "image/*")

	changeCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		files := input.Get("files")
		if !files.Truthy() || files.Length() == 0 {
			return nil
		}
		file := files.Index(0)
		reader := js.Global().Get("FileReader").New()
		loadReaderCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			dataURL := reader.Get("result").String()
			img := js.Global().Get("Image").New()
			loadImgCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				width := this.Get("naturalWidth").Float()
				height := this.Get("naturalHeight").Float()
				a.importBitmapSymbol(file.Get("name").String(), dataURL, width, height)
				return nil
			})
			errorImgCb := js.FuncOf(func(this js.Value, args []js.Value) any {
				a.statusEl.Set("textContent", "Import failed: could not load image")
				return nil
			})
			a.holdCallback(loadImgCb)
			a.holdCallback(errorImgCb)
			img.Call("addEventListener", "load", loadImgCb)
			img.Call("addEventListener", "error", errorImgCb)
			img.Set("src", dataURL)
			return nil
		})
		errorReaderCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			a.statusEl.Set("textContent", "Import failed: could not read image")
			return nil
		})
		a.holdCallback(loadReaderCb)
		a.holdCallback(errorReaderCb)
		reader.Call("addEventListener", "load", loadReaderCb)
		reader.Call("addEventListener", "error", errorReaderCb)
		reader.Call("readAsDataURL", file)
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
			a.closeShapeToolMenu()
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
				a.closeShapeToolMenu()
				a.setActiveTool(tool)
			}
			return nil
		})
		b.Call("addEventListener", "click", cb)
	}
	if a.shapeToolCorner.Truthy() {
		shapeCornerCb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("preventDefault")
				args[0].Call("stopPropagation")
			}
			if a.shapeToolMenu.Truthy() && a.shapeToolMenu.Get("classList").Call("contains", "open").Bool() {
				a.closeShapeToolMenu()
			} else {
				a.openShapeToolMenu()
			}
			return nil
		})
		a.holdCallback(shapeCornerCb)
		a.shapeToolCorner.Call("addEventListener", "click", shapeCornerCb)
	}
	if a.shapeToolMenu.Truthy() {
		shapeItems := a.shapeToolMenu.Call("querySelectorAll", "[data-shape-subtool]")
		for i := 0; i < shapeItems.Length(); i++ {
			item := shapeItems.Index(i)
			cb := js.FuncOf(func(this js.Value, args []js.Value) any {
				if len(args) > 0 {
					args[0].Call("preventDefault")
					args[0].Call("stopPropagation")
				}
				a.setShapeSubtool(this.Get("dataset").Get("shapeSubtool").String())
				a.closeShapeToolMenu()
				a.setActiveTool("shape")
				return nil
			})
			a.holdCallback(cb)
			item.Call("addEventListener", "click", cb)
		}
	}
	tabs := d.Call("querySelectorAll", ".tab")
	for i := 0; i < tabs.Length(); i++ {
		tab := tabs.Index(i)
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			panel := this.Get("dataset").Get("panel").String()
			if panel != "" {
				a.setRightPanelTab(panel)
			}
			return nil
		})
		a.holdCallback(cb)
		tab.Call("addEventListener", "click", cb)
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
	bindShapeNum(a.propStrokeW, "strokeW")
	openPicker := func(btn js.Value, field string) {
		if !btn.Truthy() {
			return
		}
		cb := js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("preventDefault")
				args[0].Call("stopPropagation")
			}
			color := "#66e3ff"
			switch field {
			case "toolFill":
				color = a.shapeToolFill
			case "toolStroke":
				color = a.shapeToolStroke
			default:
				layers := a.currentLayers()
				for _, pair := range a.selectedInstancePairsOrPrimary() {
					li, ii := pair[0], pair[1]
					if li < 0 || li >= len(layers) || ii < 0 || ii >= len(layers[li].Instances) {
						continue
					}
					inst := layers[li].Instances[ii]
					if inst.ElementType == "path" {
						if p, ok := a.findPathByID(inst.ElementID); ok {
							if field == "fill" {
								color = p.Fill
							} else {
								color = p.Stroke
							}
							break
						}
					}
					if inst.ElementType == "circle" {
						if c, ok := a.findCircleByID(inst.ElementID); ok {
							if field == "fill" {
								color = c.Fill
							} else {
								color = c.Stroke
							}
							break
						}
					}
				}
			}
			a.openColorPicker(field, btn, color)
			return nil
		})
		a.holdCallback(cb)
		btn.Call("addEventListener", "click", cb)
	}
	openPicker(a.propFill, "fill")
	openPicker(a.propStroke, "stroke")
	openPicker(a.propDocBg, "docBg")
	openPicker(a.toolFill, "toolFill")
	openPicker(a.toolStroke, "toolStroke")
	colorPickerStopCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("stopPropagation")
		}
		return nil
	})
	colorPickerInputCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyActiveColorPicker()
		return nil
	})
	alphaMirrorCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.colorPickerAlphaValue.Set("value", this.Get("value").String())
		a.applyActiveColorPicker()
		return nil
	})
	alphaNumberCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.colorPickerAlpha.Set("value", this.Get("value").String())
		a.applyActiveColorPicker()
		return nil
	})
	a.holdCallback(colorPickerStopCb)
	a.holdCallback(colorPickerInputCb)
	a.holdCallback(alphaMirrorCb)
	a.holdCallback(alphaNumberCb)
	a.colorPickerPopover.Call("addEventListener", "click", colorPickerStopCb)
	a.colorPickerColor.Call("addEventListener", "input", colorPickerInputCb)
	a.colorPickerColor.Call("addEventListener", "change", colorPickerInputCb)
	a.colorPickerAlpha.Call("addEventListener", "input", alphaMirrorCb)
	a.colorPickerAlpha.Call("addEventListener", "change", alphaMirrorCb)
	a.colorPickerAlphaValue.Call("addEventListener", "change", alphaNumberCb)
	hexInputChangeCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyColorPickerHexInput(this.Get("value").String())
		return nil
	})
	hexInputKeyCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Get("key").String() == "Enter" {
			a.applyColorPickerHexInput(this.Get("value").String())
			this.Call("blur")
		}
		return nil
	})
	a.holdCallback(hexInputChangeCb)
	a.holdCallback(hexInputKeyCb)
	a.colorPickerPreviewText.Call("addEventListener", "change", hexInputChangeCb)
	a.colorPickerPreviewText.Call("addEventListener", "keydown", hexInputKeyCb)
	nameCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyInstanceName(this.Get("value").String())
		return nil
	})
	docNameCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyDocumentField("name", this.Get("value").String())
		return nil
	})
	docWidthCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyDocumentField("width", this.Get("value").String())
		return nil
	})
	docHeightCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyDocumentField("height", this.Get("value").String())
		return nil
	})
	docFpsCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applyDocumentField("fps", this.Get("value").String())
		return nil
	})
	a.holdCallback(nameCb)
	a.holdCallback(docNameCb)
	a.holdCallback(docWidthCb)
	a.holdCallback(docHeightCb)
	a.holdCallback(docFpsCb)
	if a.propName.Truthy() {
		a.propName.Call("addEventListener", "change", nameCb)
	}
	if a.propDocName.Truthy() {
		a.propDocName.Call("addEventListener", "change", docNameCb)
	}
	if a.propDocWidth.Truthy() {
		a.propDocWidth.Call("addEventListener", "change", docWidthCb)
	}
	if a.propDocHeight.Truthy() {
		a.propDocHeight.Call("addEventListener", "change", docHeightCb)
	}
	if a.propDocFps.Truthy() {
		a.propDocFps.Call("addEventListener", "change", docFpsCb)
	}
	easeModeCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applySelectedTweenEase(this.Get("value").String(), a.propEaseDir.Get("value").String())
		return nil
	})
	easeDirCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.applySelectedTweenEase(a.propEaseMode.Get("value").String(), this.Get("value").String())
		return nil
	})
	a.holdCallback(easeModeCb)
	a.holdCallback(easeDirCb)
	a.propEaseMode.Call("addEventListener", "change", easeModeCb)
	a.propEaseDir.Call("addEventListener", "change", easeDirCb)
	docDlgCancelCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeDocumentDialog()
		return nil
	})
	docDlgSaveCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.submitDocumentDialog()
		return nil
	})
	docDlgOverlayCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Get("target").Equal(a.docDialog) {
			a.closeDocumentDialog()
		}
		return nil
	})
	settingsDlgCancelCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeSettingsDialog()
		return nil
	})
	settingsDlgSaveCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.submitSettingsDialog()
		return nil
	})
	settingsDlgOverlayCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Get("target").Equal(a.settingsDialog) {
			a.closeSettingsDialog()
		}
		return nil
	})
	a.holdCallback(docDlgCancelCb)
	a.holdCallback(docDlgSaveCb)
	a.holdCallback(docDlgOverlayCb)
	a.holdCallback(settingsDlgCancelCb)
	a.holdCallback(settingsDlgSaveCb)
	a.holdCallback(settingsDlgOverlayCb)
	a.docDlgCancel.Call("addEventListener", "click", docDlgCancelCb)
	a.docDlgSave.Call("addEventListener", "click", docDlgSaveCb)
	a.docDialog.Call("addEventListener", "click", docDlgOverlayCb)
	a.settingsDlgCancel.Call("addEventListener", "click", settingsDlgCancelCb)
	a.settingsDlgSave.Call("addEventListener", "click", settingsDlgSaveCb)
	a.settingsDialog.Call("addEventListener", "click", settingsDlgOverlayCb)
	layerRenameCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
			args[0].Call("stopPropagation")
		}
		target := a.layerCtxTargetIdx
		a.closeLayerContextMenu()
		a.renameLayer(target)
		return nil
	})
	layerDeleteCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
			args[0].Call("stopPropagation")
		}
		target := a.layerCtxTargetIdx
		a.closeLayerContextMenu()
		a.deleteLayer(target)
		return nil
	})
	stageDeleteCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
			args[0].Call("stopPropagation")
		}
		layerIdx := a.stageCtxLayerIdx
		instIdx := a.stageCtxInstIdx
		a.closeStageContextMenu()
		a.deleteInstance(layerIdx, instIdx)
		return nil
	})
	stageConvertCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
			args[0].Call("stopPropagation")
		}
		layerIdx := a.stageCtxLayerIdx
		instIdx := a.stageCtxInstIdx
		a.closeStageContextMenu()
		if layerIdx >= 0 && instIdx >= 0 {
			a.setSingleInstanceSelection(layerIdx, instIdx)
			layers := a.currentLayersPtr()
			for i := range *layers {
				(*layers)[i].Selected = i == layerIdx
			}
			a.updateSelectedLayerLabel()
		}
		a.convertSelectedInstanceToSymbol()
		return nil
	})
	keyframeDeleteCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) > 0 {
			args[0].Call("preventDefault")
			args[0].Call("stopPropagation")
		}
		layerIdx := a.keyframeCtxLayerIdx
		frame := a.keyframeCtxFrame
		a.closeKeyframeContextMenu()
		a.deleteKeyframe(layerIdx, frame)
		return nil
	})
	a.holdCallback(layerRenameCb)
	a.holdCallback(layerDeleteCb)
	a.holdCallback(stageDeleteCb)
	a.holdCallback(stageConvertCb)
	a.holdCallback(keyframeDeleteCb)
	js.Global().Get("document").Call("getElementById", "ctx-rename-layer").Call("addEventListener", "click", layerRenameCb)
	js.Global().Get("document").Call("getElementById", "ctx-delete-layer").Call("addEventListener", "click", layerDeleteCb)
	js.Global().Get("document").Call("getElementById", "ctx-convert-to-symbol").Call("addEventListener", "click", stageConvertCb)
	js.Global().Get("document").Call("getElementById", "ctx-delete-instance").Call("addEventListener", "click", stageDeleteCb)
	js.Global().Get("document").Call("getElementById", "ctx-delete-keyframe").Call("addEventListener", "click", keyframeDeleteCb)
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
	scaleLockCb := js.FuncOf(func(this js.Value, args []js.Value) any {
		a.lockScale = !a.lockScale
		a.updateScaleLockUI()
		if a.lockScale {
			a.statusEl.Set("textContent", "Lock Scale enabled")
		} else {
			a.statusEl.Set("textContent", "Lock Scale disabled")
		}
		return nil
	})
	a.holdCallback(scaleLockCb)
	a.propScaleLock.Call("addEventListener", "click", scaleLockCb)

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
	d.Call("getElementById", "btn-autokey").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			a.autoKey = !a.autoKey
			a.updateAutoKeyUI()
			if a.autoKey {
				a.statusEl.Set("textContent", "Auto Key enabled")
			} else {
				a.statusEl.Set("textContent", "Auto Key disabled")
			}
			return nil
		}),
	)

	d.Call("getElementById", "btn-add-keyframe").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			a.addKeyframeForSelectedInstances()
			return nil
		}),
	)

	// add layer
	d.Call("getElementById", "btn-layer").Call("addEventListener", "click",
		js.FuncOf(func(this js.Value, args []js.Value) any {
			a.captureUndoSnapshot()
			layers := a.currentLayersPtr()
			for i := range *layers {
				(*layers)[i].Selected = false
			}
			n := len(*layers) + 1
			*layers = append([]Layer{{
				Name:        fmt.Sprintf("Layer %d", n),
				Description: fmt.Sprintf("User created layer %d", n),
				Color:       "#c77dff",
				Selected:    true,
				Instances:   []ElementInstance{},
			}}, (*layers)...)
			a.clearInstanceSelection()
			a.updateSelectedLayerLabel()
			return nil
		}),
	)

	// window resize
	w.Call("addEventListener", "resize", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeLayerContextMenu()
		a.resizeCanvases()
		return nil
	}))

	d.Call("addEventListener", "click", js.FuncOf(func(this js.Value, args []js.Value) any {
		a.closeLayerContextMenu()
		a.closeStageContextMenu()
		a.closeKeyframeContextMenu()
		a.closeShapeToolMenu()
		a.closeColorPicker()
		return nil
	}))
	if appEl := d.Call("getElementById", "app"); appEl.Truthy() {
		appEl.Call("addEventListener", "contextmenu", js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) > 0 {
				args[0].Call("preventDefault")
			}
			return nil
		}))
	}
	d.Call("addEventListener", "mousemove", js.FuncOf(func(this js.Value, args []js.Value) any {
		if a.dragLibrarySymbolID == "" {
			return nil
		}
		e := args[0]
		a.updateLibraryDragPosition(e.Get("clientX").Float(), e.Get("clientY").Float())
		return nil
	}))

	// keyboard
	d.Call("addEventListener", "keydown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		key := e.Get("key").String()
		mod := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()
		a.closeLayerContextMenu()
		a.closeStageContextMenu()
		a.closeKeyframeContextMenu()
		if key == "Escape" {
			a.closeShapeToolMenu()
			a.closeColorPicker()
		}
		if a.docDialog.Truthy() && a.docDialog.Get("classList").Call("contains", "open").Bool() {
			if key == "Escape" {
				e.Call("preventDefault")
				a.closeDocumentDialog()
				return nil
			}
			if key == "Enter" {
				e.Call("preventDefault")
				a.submitDocumentDialog()
				return nil
			}
		}
		if a.settingsDialog.Truthy() && a.settingsDialog.Get("classList").Call("contains", "open").Bool() {
			if key == "Escape" {
				e.Call("preventDefault")
				a.closeSettingsDialog()
				return nil
			}
			if key == "Enter" {
				e.Call("preventDefault")
				a.submitSettingsDialog()
				return nil
			}
		}
		if mod && strings.EqualFold(key, "z") {
			e.Call("preventDefault")
			a.undo()
			return nil
		}
		if mod && strings.EqualFold(key, "y") {
			e.Call("preventDefault")
			a.redo()
			return nil
		}
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
		if a.activeTool == "shape" && a.drawingShape && a.shapeSubtool == "polygon" {
			if key == "+" || key == "=" {
				e.Call("preventDefault")
				if a.shapeSides < 100 {
					a.shapeSides++
				}
				return nil
			}
			if key == "-" || key == "_" {
				e.Call("preventDefault")
				if a.shapeSides > 3 {
					a.shapeSides--
				}
				return nil
			}
		}
		if key == "Escape" && a.activeTool == "pencil" && a.penEditing {
			e.Call("preventDefault")
			a.clearPenDraft()
			a.statusEl.Set("textContent", "Pen path canceled")
		}
		return nil
	}))

	// timeline mouse events
	a.tlCanvas.Call("addEventListener", "contextmenu", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		a.closeLayerContextMenu()
		a.closeStageContextMenu()
		a.closeKeyframeContextMenu()
		if layerIdx, frame, ok := a.pickTimelineKeyframeAt(x, y); ok {
			e.Call("preventDefault")
			e.Call("stopPropagation")
			a.openKeyframeContextMenu(layerIdx, frame, e.Get("clientX").Float(), e.Get("clientY").Float())
			return nil
		}
		if x > a.headerW {
			return nil
		}
		rowTop := 14.0
		if y < rowTop {
			return nil
		}
		layerIdx := int((y - rowTop) / a.layerH)
		if layerIdx < 0 || layerIdx >= len(a.currentLayers()) {
			return nil
		}
		e.Call("preventDefault")
		e.Call("stopPropagation")
		a.openLayerContextMenu(layerIdx, e.Get("clientX").Float(), e.Get("clientY").Float())
		return nil
	}))
	a.tlCanvas.Call("addEventListener", "mousedown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		a.closeLayerContextMenu()
		a.closeKeyframeContextMenu()

		// click layer header area to select layer (Ctrl/Cmd toggles)
		if x <= a.headerW {
			rowTop := 14.0
			if y >= rowTop {
				layerIdx := int((y - rowTop) / a.layerH)
				if layerIdx >= 0 && layerIdx < len(a.currentLayers()) {
					additive := e.Get("ctrlKey").Bool() || e.Get("metaKey").Bool()
					a.selectLayer(layerIdx, additive)
					return nil
				}
			}
		}

		// click to set frame (in grid area)
		if x > a.headerW {
			if li, ii, start, end, ok := a.pickTweenSpanAt(x, y); ok {
				a.setPrimarySelection(li, ii)
				a.selectedInstances = map[string]bool{selKey(li, ii): true}
				a.setSelectedTween(li, ii, start, end)
				a.updateSelectedLayerLabel()
				return nil
			}
			a.clearTweenSelection()
			f := a.xToFrame(x)
			a.setFrame(f)
			a.draggingPH = true
			a.playing = false
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
		if a.dragLibrarySymbolID != "" {
			e := args[0]
			a.updateLibraryDragPosition(e.Get("clientX").Float(), e.Get("clientY").Float())
			if a.dragLibraryOverStage {
				a.addSymbolInstanceAsNewLayer(a.dragLibrarySymbolID, a.dragLibraryStageX, a.dragLibraryStageY)
			}
			a.clearLibraryDrag()
		}
		a.draggingPH = false
		a.dragMode = ""
		a.endHistoryBatch()
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
	a.stageCanvas.Call("addEventListener", "contextmenu", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		a.closeLayerContextMenu()
		a.closeStageContextMenu()
		if li, ii, ok := a.pickInstanceAt(x, y); ok {
			e.Call("preventDefault")
			e.Call("stopPropagation")
			a.setSingleInstanceSelection(li, ii)
			layers := a.currentLayersPtr()
			for i := range *layers {
				(*layers)[i].Selected = i == li
			}
			a.updateSelectedLayerLabel()
			a.openStageContextMenu(li, ii, e.Get("clientX").Float(), e.Get("clientY").Float())
		}
		return nil
	}))
	a.stageCanvas.Call("addEventListener", "dblclick", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		if li, ii, ok := a.pickInstanceAt(x, y); ok {
			layers := a.currentLayers()
			inst := layers[li].Instances[ii]
			if inst.ElementType == "symbol" {
				if sym, ok := a.findSymbolByID(inst.ElementID); ok && sym.SymbolType == "movieclip" {
					a.enterMovieClipTimeline(sym.ID)
					return nil
				}
			}
		}
		a.exitMovieClipTimeline()
		return nil
	}))
	if a.stageViewport.Truthy() {
		a.stageViewport.Call("addEventListener", "mousedown", js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			e := args[0]
			target := e.Get("target")
			if target.Truthy() && target.Equal(a.stageCanvas) {
				return nil
			}
			a.clearAllSelection()
			return nil
		}))
		a.stageViewport.Call("addEventListener", "dblclick", js.FuncOf(func(this js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			e := args[0]
			target := e.Get("target")
			if target.Truthy() && target.Equal(a.stageCanvas) {
				return nil
			}
			a.clearAllSelection()
			a.exitMovieClipTimeline()
			return nil
		}))
	}
	a.stageCanvas.Call("addEventListener", "mousedown", js.FuncOf(func(this js.Value, args []js.Value) any {
		e := args[0]
		x := e.Get("offsetX").Float()
		y := e.Get("offsetY").Float()
		a.closeStageContextMenu()
		a.closeShapeToolMenu()

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
						a.beginHistoryBatch()
						a.dragMode = "rotate"
						return nil
					}
					if dist(x, y, scaleX, scaleY) <= 8 {
						a.beginHistoryBatch()
						a.dragMode = "scale"
						return nil
					}
					if dist(x, y, skewXx, skewXy) <= 8 {
						a.beginHistoryBatch()
						a.dragMode = "skewX"
						return nil
					}
					if dist(x, y, skewYx, skewYy) <= 8 {
						a.beginHistoryBatch()
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
					layers := a.currentLayersPtr()
					for i := range *layers {
						(*layers)[i].Selected = i == li
					}
				}
				a.updateSelectedLayerLabel()
				a.beginHistoryBatch()
				a.dragMode = "move"
			} else {
				if !additive {
					a.clearAllSelection()
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
			inst := a.currentLayers()[a.selectedLayerIdx].Instances[a.selectedInstIdx]
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
				a.beginHistoryBatch()
				a.dragMode = "subselect"
			}
		case "shape":
			a.shapeStartX = x
			a.shapeStartY = y
			a.shapeNowX = x
			a.shapeNowY = y
			a.shapeAsStar = e.Get("ctrlKey").Bool()
			a.shapeFromCenter = e.Get("altKey").Bool()
			a.shapeUniform = e.Get("shiftKey").Bool()
			a.drawingShape = true
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

		if a.drawingShape {
			a.shapeNowX = x
			a.shapeNowY = y
			a.shapeFromCenter = e.Get("altKey").Bool()
			a.shapeUniform = e.Get("shiftKey").Bool()
		}

		if a.dragMode != "" && len(a.selectedInstancePairs()) > 0 {
			dx := x - a.lastMouseX
			dy := y - a.lastMouseY
			for _, pair := range a.selectedInstancePairs() {
				li, ii := pair[0], pair[1]
				if kf, ok := a.writableTransformKeyframe(li, ii, a.curFrame); ok {
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
						prevVX := a.lastMouseX - ax
						prevVY := a.lastMouseY - ay
						curVX := x - ax
						curVY := y - ay
						if a.lockScale {
							prevD := math.Hypot(prevVX, prevVY)
							curD := math.Hypot(curVX, curVY)
							if prevD > 1e-3 {
								s := curD / prevD
								kf.ScaleX *= s
								kf.ScaleY *= s
								a.setInstanceKeyframe(li, ii, a.curFrame, kf)
							}
						} else {
							changed := false
							if math.Abs(prevVX) > 1e-3 {
								kf.ScaleX *= curVX / prevVX
								changed = true
							}
							if math.Abs(prevVY) > 1e-3 {
								kf.ScaleY *= curVY / prevVY
								changed = true
							}
							if changed {
								a.setInstanceKeyframe(li, ii, a.curFrame, kf)
							}
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
			inst := a.currentLayers()[a.selectedLayerIdx].Instances[a.selectedInstIdx]
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
			layers := a.currentLayers()
			for li := range layers {
				for ii := range layers[li].Instances {
					inst := layers[li].Instances[ii]
					if !isRenderableInstanceType(inst) {
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
		if a.drawingShape {
			a.commitShapeDraft()
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
	if a.activeTool == "shape" && tool != "shape" {
		a.drawingShape = false
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
		"pencil":    "Pencil",
		"line":      "Line",
		"tween":     "Classic Tween",
		"action":    "Action Script",
	}[tool]
	if tool == "shape" {
		if a.shapeSubtool == "oval" {
			name = "Shape: Oval"
		} else if a.shapeSubtool == "polygon" {
			name = "Shape: Polygon"
		} else {
			name = "Shape: Rectangle"
		}
	}
	if name == "" {
		name = tool
	}
	a.selToolEl.Set("textContent", name)
	if tool == "shape" || tool == "pencil" {
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
		a.playAccum = 0
		return
	}

	advance := float64(dt) / float64(time.Second) * float64(a.doc.FPS)
	if advance <= 0 {
		return
	}

	a.playAccum += advance
	for a.playAccum >= 1 {
		a.curFrame++
		if a.curFrame > a.doc.TotalFrames {
			a.curFrame = 1
		}
		a.playAccum -= 1
	}

}

func (a *App) setFrame(f int) {
	if f < 1 {
		f = 1
	}
	if f > a.doc.TotalFrames {
		f = a.doc.TotalFrames
	}
	a.dragMode = ""
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
	ctx.Set("fillStyle", a.doc.Background)
	ctx.Call("fillRect", 0, 0, w, h)

	// draw shape instances
	layers := a.currentLayers()
	for li := range layers {
		layer := layers[li]
		for ii := range layer.Instances {
			inst := layer.Instances[ii]
			a.drawInstanceRecursive(ctx, inst, a.curFrame, 0)
		}
	}

	if a.drawingShape {
		var (
			preview []BezierPoint
			cx      float64
			cy      float64
		)
		switch a.shapeSubtool {
		case "oval":
			if pts, px, py, ok := ovalPathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeFromCenter, a.shapeUniform); ok {
				preview = pts
				cx = px
				cy = py
			}
		case "polygon":
			if pts, px, py, ok := polygonPathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeSides, a.shapeAsStar, a.shapeFromCenter, a.shapeUniform); ok {
				preview = pts
				cx = px
				cy = py
			}
		default:
			if pts, px, py, ok := rectanglePathPoints(a.shapeStartX, a.shapeStartY, a.shapeNowX, a.shapeNowY, a.shapeFromCenter, a.shapeUniform); ok {
				preview = pts
				cx = px
				cy = py
			}
		}
		if len(preview) >= 3 {
			ctx.Call("save")
			ctx.Call("translate", cx, cy)
			drawPathLocal(ctx, VectorPath{
				Points:  preview,
				Stroke:  "#66e3ff",
				Fill:    "rgba(102, 227, 255, 0.25)",
				StrokeW: 2,
				Closed:  true,
			})
			ctx.Call("restore")
		}
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

	if a.dragLibrarySymbolID != "" && a.dragLibraryOverStage {
		name := a.symbolNameByID(a.dragLibrarySymbolID)
		ctx.Set("fillStyle", "rgba(255, 204, 102, 0.18)")
		ctx.Call("fillRect", a.dragLibraryStageX-40, a.dragLibraryStageY-20, 80, 40)
		ctx.Set("strokeStyle", "rgba(255, 204, 102, 0.95)")
		ctx.Set("lineWidth", 1.5)
		ctx.Call("strokeRect", a.dragLibraryStageX-40, a.dragLibraryStageY-20, 80, 40)
		ctx.Set("fillStyle", "rgba(255,255,255,0.95)")
		ctx.Set("font", "12px system-ui")
		ctx.Call("fillText", name, a.dragLibraryStageX-34, a.dragLibraryStageY+4)
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
		inst := layers[a.selectedLayerIdx].Instances[a.selectedInstIdx]
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
	layers := a.currentLayers()
	for i, layer := range layers {
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

		if a.selectedLayerIdx == i && a.selectedInstIdx >= 0 && a.selectedInstIdx < len(layer.Instances) {
			frames := a.selectedInstanceTweenFrames(i, a.selectedInstIdx)
			keyW := math.Max(6, a.zoom-4)
			for fi := 0; fi < len(frames)-1; fi++ {
				start := frames[fi]
				end := frames[fi+1]
				x0 := a.frameToX(start) + 2 + keyW
				x1 := a.frameToX(end) + 2
				if x1 <= x0 {
					continue
				}
				if a.selectedTweenLayerIdx == i && a.selectedTweenInstIdx == a.selectedInstIdx &&
					a.selectedTweenStartFrame == start && a.selectedTweenEndFrame == end {
					ctx.Set("fillStyle", "rgba(255, 204, 102, 0.9)")
				} else {
					ctx.Set("fillStyle", "rgba(255,255,255,0.24)")
				}
				ctx.Call("fillRect", x0, y-6, x1-x0, 6)
			}
		}

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
		a.timelinePath = nil
		a.syncBitmapAssets()
		a.undoStack = nil
		a.redoStack = nil
		a.clearInstanceSelection()
		a.setFrame(1)
		a.playing = false
		a.refreshDocUI()
		a.statusEl.Set("textContent", "New document")

	case "file.open":
		if err := a.openDocumentFromDisk(); err != nil {
			a.statusEl.Set("textContent", "Open failed: "+err.Error())
			return
		}
		a.undoStack = nil
		a.redoStack = nil
		a.statusEl.Set("textContent", "Choose a .json document to open")

	case "file.save":
		if err := a.saveDocumentToDisk(); err != nil {
			a.statusEl.Set("textContent", "Save failed: "+err.Error())
			return
		}

	case "file.importImage":
		if err := a.importImageFromDisk(); err != nil {
			a.statusEl.Set("textContent", "Import failed: "+err.Error())
			return
		}
		a.statusEl.Set("textContent", "Choose an image to import")

	case "file.export":
		a.statusEl.Set("textContent", "Export requested")
		js.Global().Call("alert", "Export hook clicked")

	case "edit.undo":
		a.undo()

	case "edit.redo":
		a.redo()

	case "edit.settings":
		a.openSettingsDialog()

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
		a.captureUndoSnapshot()
		layers := a.currentLayersPtr()
		for i := range *layers {
			(*layers)[i].Selected = false
		}
		n := len(*layers) + 1
		*layers = append([]Layer{{
			Name:        fmt.Sprintf("Layer %d", n),
			Description: fmt.Sprintf("User created layer %d", n),
			Color:       "#c77dff",
			Selected:    true,
			Instances:   []ElementInstance{},
		}}, (*layers)...)
		a.clearInstanceSelection()
		a.updateSelectedLayerLabel()
		a.statusEl.Set("textContent", "Layer added")

	case "insert.keyframe":
		a.addKeyframeForSelectedInstances()

	case "insert.blankKeyframe":
		a.addKeyframeForSelectedInstances()

	case "modify.convertToSymbol":
		a.convertSelectedInstanceToSymbol()

	case "modify.breakApart":
		a.statusEl.Set("textContent", "Break Apart requested")

	case "modify.document":
		a.openDocumentDialog()

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
		a.setRightPanelTab("properties")
		a.statusEl.Set("textContent", "Properties panel opened")

	case "window.library":
		a.setRightPanelTab("library")
		a.statusEl.Set("textContent", "Library panel opened")

	case "help.about":
		js.Global().Call("alert", "Animate-like Editor\nBuilt with Go + WASM")
		a.statusEl.Set("textContent", "About opened")

	default:
		a.statusEl.Set("textContent", "Unhandled action: "+action)
	}
}
