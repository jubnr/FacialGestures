// Copyright (2026) Christophe Pallier <christophe@pallier.org>
// Distributed under the GNU General Public License v3.

// FacialGestures — facial-gesture task (replication of Tuomaala et al., 2025).
//
// Preliminary MEG paradigm: the participant produces 10 facial gestures, each
// preferentially activating one muscle (O'Dwyer et al., 1981), to collect "pure"
// muscle artefacts (with simultaneous EMG). These are then used to remove speech
// artefacts automatically (ICA + mutual information).
//
// Structure (Fig. 2 of the paper):
//
//	sets × [set instructions 5 s → blank 1 s → 10 gestures]                          = 166 s / set
//	10 gestures (new random order each set) × [instructions 5 s → blank 1 s → 5 reps] = 16 s / gesture
//	5 repetitions × [10 ms beep ("go") + 1990 ms execution window]                    = 2 s / repetition
//
// Shortened version used by default (the paper's 5 sets = 830 s were too long for
// our session): 3 sets, and the gesture instructions last 3 s instead of 5 s from
// set 2 on, when the participant already knows the gestures. Total 458 s
// (7 min 38 s), 15 repetitions per gesture. Paper timing: --sets 5 --later-instr-ms 5000.
//
// Timing: the whole run follows an ABSOLUTE schedule anchored on the run-start
// flip (trigger 100). Every screen and beep is presented on the flip closest to
// its nominal time, so delays never accumulate and the TTL pulse width needs no
// manual compensation.
//
// Triggers: parallel port, same code and conventions as
// examples/MindSentencesExtension (trigger sent right AFTER the flip that starts
// the sound, blocking pulse, null device if the port cannot be opened).
//
// Reference:
//
//	Tuomaala, S., Autti, S., Cotroneo, S.F., Lioumis, P., Renvall, H., Liljeström, M.
//	(2025). Automated speech artefact removal from MEG data utilizing facial
//	gestures and mutual information. Imaging Neuroscience, 3, imag_a_00545.
//	https://doi.org/10.1162/imag_a_00545
//
// Usage:
//
//	go run . -w -s 1 --trigger none --sets 1     # quick test outside the MEG room
//	./FacialGestures -s 1                        # in the MEG room
package main

import (
	"encoding/binary"
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chrplr/goxpyriment/apparatus"
	"github.com/chrplr/goxpyriment/control"
	"github.com/chrplr/goxpyriment/stimuli"
)

// ── Timing (Fig. 2 of Tuomaala et al., 2025) ─────────────────────────────────

const (
	NGestures    = 10 // Fig. 2: 10 gestures per set
	NRepetitions = 5  // Fig. 2: 5 consecutive repetitions per gesture

	SetInstrMS     = 5000 // Fig. 2: set instructions, 5 s
	SetBlankMS     = 1000 // Fig. 2: blank screen after set instructions, 1 s
	GestureInstrMS = 5000 // Fig. 2: gesture instructions, 5 s
	GestureBlankMS = 1000 // Fig. 2: blank screen after gesture instructions, 1 s
	BeepMS         = 10   // Fig. 2: auditory "go" stimulus, 10 ms
	ExecWindowMS   = 1990 // Fig. 2: execution window after the beep, 1990 ms

	RepetitionMS = BeepMS + ExecWindowMS // Fig. 2: 2000 ms per repetition

	// Not in the paper: delay between the run-start trigger (100) and the first
	// set screen, so that codes 100 and 21 do not overlap on the STI channel.
	LeadInMS = 1000

	// Not in the paper (shortened version): defaults for --sets and for the
	// gesture-instruction duration from set 2 on.
	DefaultSets         = 3
	DefaultLaterInstrMS = 3000
)

// gestureMS is the duration of one gesture block (Fig. 2: 16 s with 5 s instructions).
func gestureMS(instrMS int) int { return instrMS + GestureBlankMS + NRepetitions*RepetitionMS }

// setMS is the duration of one set (Fig. 2: 166 s with 5 s gesture instructions).
func setMS(instrMS int) int { return SetInstrMS + SetBlankMS + NGestures*gestureMS(instrMS) }

// ── Beep ─────────────────────────────────────────────────────────────────────

const (
	BeepFreqHz     = 1000.0 // ~1 kHz (frequency not given in the paper)
	BeepRampMS     = 2      // onset/offset ramps (avoid clicks)
	BeepAmplitude  = 0.8    // 0–1; set the actual level on the audio chain
	BeepSampleRate = 44100
)

// ── Triggers ─────────────────────────────────────────────────────────────────
//
// Code plan (8 bits). The event of interest is the BEEP, not the flip: the
// repetition trigger is sent right after the flip that starts the sound
// (PlaySyncedWithFlip). The trigger→actual-sound delay depends on the room's
// audio chain and MUST be measured on site (microphone/sensor on a MISC
// channel), then subtracted at analysis. On the goxpyriment test bench a
// TTL→audio lag of about 100 ms (SD 6 ms) was reported: do not reuse that value
// as is in the MEG.

const (
	TrigRepBase     byte = 0   // 1..10  : repetition onset (code = gesture number)
	TrigGestureBase byte = 10  // 11..20 : gesture-instruction onset (10 + gesture number)
	TrigSetBase     byte = 20  // 21..29 : set-instruction onset (20 + set number)
	TrigRunStart    byte = 100 // run start (MindSentences convention)
	TrigRunEnd      byte = 101 // run end (MindSentences convention)
)

// ── Gestures (O'Dwyer et al., 1981) ──────────────────────────────────────────

type gesture struct {
	num    int    // 1..10
	id     string // G1..G10
	muscle string
	label  string // instruction shown to the participant
}

var gestures = [NGestures]gesture{
	{1, "G1", "Levator labii superioris", "Raise your upper lip on one side only (sneer)"},
	{2, "G2", "Zygomaticus major", "Smile broadly"},
	{3, "G3", "Buccinator", "Puff out your cheeks, lips closed"},
	{4, "G4", "Risorius", "Smile broadly with your lips closed"},
	{5, "G5", "Orbicularis oris superioris", "Press your upper lip against your upper front teeth"},
	{6, "G6", "Orbicularis oris inferioris", "Press your lower lip against your lower front teeth"},
	{7, "G7", "Depressor labii inferioris", "Pull your lower lip down, keeping your jaw closed"},
	{8, "G8", "Depressor anguli oris", "Pull the corners of your mouth down"},
	{9, "G9", "Mentalis", "Push your lower lip up and out, wrinkling your chin"},
	{10, "G10", "Genioglossus", "Press your tongue against the roof of your mouth"},
}

// ── Single trigger entry point ───────────────────────────────────────────────

var (
	trigPort    *parPort      // nil = triggers disabled (null device)
	trigPulse   time.Duration // pulse width
	trigErrShow = true        // warn only once on write errors
)

// openTrigger opens the requested backend; on failure it warns and continues
// with a null device (as in MindSentencesExtension).
func openTrigger(backend, device string) {
	switch backend {
	case "none":
		log.Println("Triggers disabled (--trigger none).")
	case "parport", "parallel":
		pp, err := openParPort(device)
		if err != nil {
			log.Printf("WARNING: cannot open trigger parallel port %s: %v\n"+
				"         → triggers DISABLED. (sudo modprobe ppdev ; sudo usermod -aG lp $USER ?)", device, err)
			return
		}
		log.Printf("Trigger parallel port opened: %s", device)
		trigPort = pp
	default:
		log.Printf("Unknown trigger backend %q → triggers disabled.", backend)
	}
}

// sendTrigger is the ONLY place that emits a code. It raises the lines, holds
// the pulse for trigPulse (blocking), then sets them back to 0. It returns the
// SDL timestamp (ns) taken right after the lines went up (0 if disabled).
func sendTrigger(code byte) uint64 {
	if trigPort == nil {
		return 0
	}
	if err := trigPort.SetData(code); err != nil {
		if trigErrShow {
			log.Printf("WARNING: trigger write failed (code %d): %v — continuing", code, err)
			trigErrShow = false
		}
		return 0
	}
	ts := control.TicksNS()
	time.Sleep(trigPulse)
	_ = trigPort.SetData(0)
	return ts
}

// ── Beep generation (16-bit PCM mono WAV in memory) ──────────────────────────

func makeBeepWAV() []byte {
	n := BeepSampleRate * BeepMS / 1000
	ramp := BeepSampleRate * BeepRampMS / 1000
	pcm := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		env := 1.0
		if i < ramp { // raised-cosine onset ramp
			env = 0.5 - 0.5*math.Cos(math.Pi*float64(i)/float64(ramp))
		} else if i >= n-ramp { // raised-cosine offset ramp
			env = 0.5 - 0.5*math.Cos(math.Pi*float64(n-1-i)/float64(ramp))
		}
		v := BeepAmplitude * env * math.Sin(2*math.Pi*BeepFreqHz*float64(i)/BeepSampleRate)
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(v*math.MaxInt16)))
	}

	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)               // fmt chunk size
	binary.LittleEndian.PutUint16(h[20:], 1)                // PCM
	binary.LittleEndian.PutUint16(h[22:], 1)                // mono
	binary.LittleEndian.PutUint32(h[24:], BeepSampleRate)   // sample rate
	binary.LittleEndian.PutUint32(h[28:], BeepSampleRate*2) // bytes/s
	binary.LittleEndian.PutUint16(h[32:], 2)                // bytes/frame
	binary.LittleEndian.PutUint16(h[34:], 16)               // bits/sample
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}

// ── Gesture instruction screen (text + schematic) ────────────────────────────

// composite draws several stimuli as a single screen.
type composite struct {
	stimuli.BaseVisual
	parts []stimuli.VisualStimulus
}

func (c *composite) Draw(screen *apparatus.Screen) error {
	for _, p := range c.parts {
		if err := p.Draw(screen); err != nil {
			return err
		}
	}
	return nil
}

func (c *composite) Present(screen *apparatus.Screen, clear, update bool) error {
	return stimuli.PresentDrawable(c, screen, clear, update)
}

func (c *composite) preload(screen *apparatus.Screen) {
	for _, p := range c.parts {
		if err := stimuli.PreloadVisualOnScreen(screen, p); err != nil {
			log.Printf("preload: %v", err)
		}
	}
}

// ── Event log (.tsv) ─────────────────────────────────────────────────────────

// eventRow describes a timestamped event. Empty fields are written as "n/a".
type eventRow struct {
	event      string
	durationMS int  // nominal duration of the event (ms)
	code       byte // 0 = no trigger
	set        int  // 1..nSets (0 = n/a)
	gesturePos int  // 1..10 (0 = n/a)
	g          *gesture
	repetition int     // 1..5 (0 = n/a)
	schedNS    uint64  // nominal onset (SDL clock)
	flipNS     uint64  // actual flip (SDL clock)
	trigNS     uint64  // trigger rise (0 = none)
	periodMS   float64 // realised period since the previous repetition (<0 = n/a)
}

type eventLog struct {
	f      *os.File
	w      *csv.Writer
	subj   int
	seed   int64
	t0NS   uint64 // run-start flip (onset = 0)
	nextID int
}

// newEventLog creates the TSV; refuses to overwrite an existing file (as MindSentences).
func newEventLog(path string, subj int, seed int64) (*eventLog, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("output already exists: %s — refusing to overwrite", path)
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	w := csv.NewWriter(f)
	w.Comma = '\t'
	_ = w.Write([]string{
		// BIDS columns first (onset/duration in s from trigger 100)
		"onset", "duration", "trial_type", "value",
		"event_index", "subject_id", "seed",
		"set", "gesture_position", "gesture_id", "gesture_number", "muscle", "gesture_label", "repetition",
		"scheduled_onset_s", "onset_error_ms", "period_ms",
		"t_flip_ns", "t_trigger_ns", "trigger_minus_flip_ms",
	})
	w.Flush()
	return &eventLog{f: f, w: w, subj: subj, seed: seed}, w.Error()
}

func na(ok bool, s string) string {
	if !ok {
		return "n/a"
	}
	return s
}

func (el *eventLog) write(r eventRow) {
	el.nextID++
	rel := func(ns uint64) string { return fmt.Sprintf("%.4f", float64(int64(ns)-int64(el.t0NS))/1e9) }
	ms := func(d int64) string { return fmt.Sprintf("%.3f", float64(d)/1e6) }

	gid, gnum, muscle, label := "n/a", "n/a", "n/a", "n/a"
	if r.g != nil {
		gid, gnum, muscle, label = r.g.id, strconv.Itoa(r.g.num), r.g.muscle, r.g.label
	}
	_ = el.w.Write([]string{
		rel(r.flipNS),
		fmt.Sprintf("%.3f", float64(r.durationMS)/1000),
		r.event,
		na(r.code != 0, strconv.Itoa(int(r.code))),
		strconv.Itoa(el.nextID), strconv.Itoa(el.subj), strconv.FormatInt(el.seed, 10),
		na(r.set > 0, strconv.Itoa(r.set)),
		na(r.gesturePos > 0, strconv.Itoa(r.gesturePos)),
		gid, gnum, muscle, label,
		na(r.repetition > 0, strconv.Itoa(r.repetition)),
		na(r.schedNS > 0, rel(r.schedNS)),
		na(r.schedNS > 0, ms(int64(r.flipNS)-int64(r.schedNS))),
		na(r.periodMS >= 0, fmt.Sprintf("%.3f", r.periodMS)),
		strconv.FormatUint(r.flipNS, 10),
		na(r.trigNS > 0, strconv.FormatUint(r.trigNS, 10)),
		na(r.trigNS > 0, ms(int64(r.trigNS)-int64(r.flipNS))),
	})
	el.w.Flush() // nothing is lost on abort (Escape)
}

func (el *eventLog) close() {
	el.w.Flush()
	_ = el.f.Close()
}

// ── Output folder ────────────────────────────────────────────────────────────

// resolveDataDir returns (and creates) the output folder: --data-dir if given,
// otherwise a data/ folder inside the experiment folder. That folder is the one
// holding the binary, or, under `go run` (binary in a temporary build dir), the
// one holding this source file. Last resort: ./data.
func resolveDataDir(configured string) string {
	var candidates []string
	if configured != "" {
		candidates = append(candidates, configured)
	} else {
		if exe, err := os.Executable(); err == nil && !strings.HasPrefix(exe, os.TempDir()) &&
			!strings.Contains(exe, "go-build") {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "data"))
		}
		if _, src, _, ok := runtime.Caller(0); ok {
			if _, err := os.Stat(src); err == nil {
				candidates = append(candidates, filepath.Join(filepath.Dir(src), "data"))
			}
		}
		candidates = append(candidates, "data")
	}
	for _, dir := range candidates {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if abs, err := filepath.Abs(dir); err == nil {
				return abs
			}
			return dir
		}
		log.Printf("WARNING: cannot create data folder %s", dir)
	}
	return "data"
}

// ── Main ─────────────────────────────────────────────────────────────────────

func main() {
	// Experiment flags. The standard -w/-d/-s flags are registered here too (instead
	// of using NewExperimentFromFlags) so the output folder can be set before
	// Initialize() creates the data file.
	triggerFlag := flag.String("trigger", "parport", "Trigger backend: parport | none")
	parportFlag := flag.String("parport", "/dev/parport1", "Parallel port device (trigger output)")
	pulseFlag := flag.Int("pulse-ms", 5, "TTL pulse width (ms)")
	setsFlag := flag.Int("sets", DefaultSets, "Number of sets (paper: 5; use 1 for a quick test)")
	laterInstrFlag := flag.Int("later-instr-ms", DefaultLaterInstrMS, "Gesture-instruction duration from set 2 on, in ms (paper: 5000)")
	seedFlag := flag.Int64("seed", 0, "Random seed for gesture order (0 = clock)")

	dataDirFlag := flag.String("data-dir", "", "Output folder (default: the data/ folder next to this experiment)")
	windowedFlag := flag.Bool("w", false, "Windowed mode (1024×768 window instead of fullscreen)")
	displayFlag := flag.Int("d", -1, "Display ID: monitor index (-1 = primary)")
	subjectFlag := flag.Int("s", 0, "Subject ID")
	flag.Parse()

	width, height, fullscreen := 0, 0, true
	if *windowedFlag {
		width, height, fullscreen = 1024, 768, false
	}
	exp := control.NewExperiment("FacialGestures", width, height, fullscreen, control.Black, control.White, 32)
	exp.SubjectID = *subjectFlag
	if *displayFlag >= 0 {
		exp.ScreenNumber = *displayFlag
	}
	dataDir := resolveDataDir(*dataDirFlag)
	exp.SetOutputDirectory(dataDir)
	if err := exp.Initialize(); err != nil {
		log.Fatalf("failed to initialize experiment: %v", err)
	}
	defer exp.End()
	log.Printf("Output folder: %s", dataDir)

	nSets := *setsFlag
	if nSets < 1 || nSets > 9 {
		exp.Fatal("--sets must be in 1..9 (set trigger codes are 21..29), got %d", nSets)
	}
	if *laterInstrFlag < 1000 {
		exp.Fatal("--later-instr-ms must be at least 1000, got %d", *laterInstrFlag)
	}
	seed := *seedFlag
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))

	// Gesture-instruction duration and onset of each set, relative to t0.
	instrMS := make([]int, nSets)
	setOffsetMS := make([]int, nSets+1) // setOffsetMS[nSets] = total duration
	for s := 0; s < nSets; s++ {
		instrMS[s] = GestureInstrMS
		if s > 0 {
			instrMS[s] = *laterInstrFlag
		}
		setOffsetMS[s+1] = setOffsetMS[s] + setMS(instrMS[s])
	}
	totalS := setOffsetMS[nSets] / 1000
	trigPulse = time.Duration(*pulseFlag) * time.Millisecond

	openTrigger(*triggerFlag, *parportFlag)
	defer func() {
		if trigPort != nil {
			trigPort.Close()
		}
	}()

	// .tsv file: every event, in the goxpyriment data folder.
	// Same base name as the .csv (sub-XXX_date-YYYYMMDD-HHMM), so the files of one
	// session go together and a relaunch never collides with a previous one.
	tsvPath := strings.TrimSuffix(exp.Data.FullPath, ".csv") + "_events.tsv"
	evlog, err := newEventLog(tsvPath, exp.SubjectID, seed)
	if err != nil {
		exp.Fatal("%v", err)
	}
	defer evlog.close()
	log.Printf("Events TSV: %s", tsvPath)

	triggerStatus := "DISABLED (no port open)"
	if trigPort != nil {
		triggerStatus = "parallel port " + *parportFlag + " — READY"
	}
	exp.Data.WriteComment("paper: Tuomaala et al. (2025) Imaging Neuroscience 3, imag_a_00545")
	exp.Data.WriteComment(fmt.Sprintf("sets=%d later_instr_ms=%d seed=%d trigger=%s device=%s pulse_ms=%d",
		nSets, *laterInstrFlag, seed, *triggerFlag, *parportFlag, *pulseFlag))
	exp.Data.WriteComment("events_tsv=" + tsvPath)
	exp.Data.AddVariableNames([]string{
		"event", "set", "gesture_position", "gesture_id", "muscle", "repetition",
		"trigger_code", "t_onset_ns", "t_trigger_ns", "onset_run_s", "period_ms",
	})
	logData := func(event string, set, pos int, g *gesture, rep int, code byte, flipNS, trigNS uint64, periodMS float64) {
		gid, muscle := "", ""
		if g != nil {
			gid, muscle = g.id, g.muscle
		}
		period := ""
		if periodMS >= 0 {
			period = fmt.Sprintf("%.3f", periodMS)
		}
		exp.Data.Add(event, set, pos, gid, muscle, rep, int(code), flipNS, trigNS,
			float64(int64(flipNS)-int64(evlog.t0NS))/1e9, period)
	}

	// ── Stimuli (preloaded so no texture is created right before a flip) ──
	beep := stimuli.NewSoundFromMemory(makeBeepWAV())
	if err := beep.PreloadDevice(exp.AudioDevice); err != nil {
		exp.Fatal("cannot load beep: %v", err)
	}
	fix := stimuli.NewFixCross(40, 4, control.White)
	blank := stimuli.NewBlankScreen(control.Black)

	W, H := float32(exp.Screen.Width), float32(exp.Screen.Height)
	boxW := int32(W * 0.8)
	if boxW < 400 {
		boxW = 400
	}
	text := func(s string, y float32) *stimuli.TextBox {
		tb := stimuli.NewTextBox(s, boxW, control.Point(0, y), control.White)
		_ = stimuli.PreloadVisualOnScreen(exp.Screen, tb)
		return tb
	}

	setScreens := make([]*stimuli.TextBox, nSets)
	for s := range setScreens {
		setScreens[s] = text(fmt.Sprintf(
			"Set %d / %d\n\n"+
				"You will make 10 facial gestures, 5 times each.\n\n"+
				"At each beep, make the gesture once, then relax.\n"+
				"Keep your eyes on the cross and your head still.", s+1, nSets), 0)
	}

	// Gesture screens: instruction at the top, schematic ("at rest → gesture") below.
	margin := 0.05 * H
	schemPNG := make([][]byte, NGestures)
	var wg sync.WaitGroup
	gestureScreens := make([]*composite, NGestures)
	type layout struct {
		tb                     *stimuli.TextBox
		imgW, imgH, imgY, capY float32
	}
	layouts := make([]layout, NGestures)
	for i, g := range gestures {
		tb := text("Next gesture:\n\n"+g.label, 0)
		top := H/2 - margin
		tb.SetPosition(control.Point(0, top-tb.Height/2))
		capH := 1.6 * float32(exp.Screen.DefaultFont.Height())
		imgTop := top - tb.Height - margin/2
		imgH := imgTop - (-H/2 + margin) - capH
		imgW := imgH * schemUnitsW / schemUnitsH
		if imgW > 0.95*W {
			imgW = 0.95 * W
			imgH = imgW * schemUnitsH / schemUnitsW
		}
		layouts[i] = layout{tb, imgW, imgH, imgTop - imgH/2, imgTop - imgH - capH/2}
		wg.Add(1)
		go func(i int, w, h int) { // rendering takes ~0.5 s per image: do it in parallel
			defer wg.Done()
			schemPNG[i] = renderSchematic(i, w, h)
		}(i, int(imgW), int(imgH))
	}
	wg.Wait()
	for i, l := range layouts {
		pic := stimuli.NewPictureFromMemory(schemPNG[i], 0, l.imgY)
		pic.Width, pic.Height = float32(int(l.imgW)), float32(int(l.imgH))
		panelX := func(u float32) float32 { return (u - schemUnitsW/2) * l.imgW / schemUnitsW }
		gestureScreens[i] = &composite{parts: []stimuli.VisualStimulus{
			l.tb, pic,
			stimuli.NewTextLine("At rest", panelX(panelLeftX+panelW/2), l.capY, control.Gray),
			stimuli.NewTextLine("Gesture", panelX(panelRightX+panelW/2), l.capY, control.Gray),
		}}
		gestureScreens[i].preload(exp.Screen)
	}

	frame := uint64(exp.Screen.FrameDuration())
	msNS := func(ms int) uint64 { return uint64(ms) * uint64(time.Millisecond) }

	// waitUntil pumps events (Escape handled by exp.Wait) until half a frame
	// before the deadline, so the next flip lands as close to it as possible.
	waitUntil := func(target uint64) {
		for control.TicksNS()+frame/2 < target {
			exp.Wait(1)
		}
	}
	// showAt presents a screen at its deadline, sends the trigger (0 = none) and logs it.
	showAt := func(stim stimuli.VisualStimulus, r eventRow) eventRow {
		waitUntil(r.schedNS)
		flipNS, err := exp.ShowTS(stim)
		if err != nil {
			log.Printf("flip error (%s): %v", r.event, err)
		}
		r.flipNS = flipNS
		if r.code != 0 {
			r.trigNS = sendTrigger(r.code)
		}
		r.periodMS = -1
		evlog.write(r)
		return r
	}

	completed := false
	runErr := exp.Run(func() error {
		_ = exp.HideCursor()

		// Experimenter screen: hardware status (as in MindSentences).
		if err := exp.ShowInstructions(fmt.Sprintf(
			"[Experimenter]\n\n"+
				"Subject %d — %d set(s) — duration %d min %02d s\n\n"+
				"Triggers: %s\n\n"+
				"Check: EMG/EOG/ECG, beep audible in the room, MEG acquisition running.\n\n"+
				"SPACE: show the participant instructions",
			exp.SubjectID, nSets,
			totalS/60, totalS%60, triggerStatus)); err != nil {
			return err
		}

		if err := exp.ShowInstructions(
			"In this task you will make gestures with your face.\n\n" +
				"Before each series, a screen will show you which gesture to make.\n" +
				"Then a cross will appear: at each beep, make the gesture once,\n" +
				"then relax completely until the next beep.\n\n" +
				"Keep your eyes on the cross, keep your head still, and do not speak.\n\n" +
				"The experimenter will start the task."); err != nil {
			return err
		}

		// Run start: trigger 100 on a blank screen, then the absolute schedule.
		runFlip, _ := exp.ShowTS(blank)
		evlog.t0NS = runFlip
		trig := sendTrigger(TrigRunStart)
		evlog.write(eventRow{event: "RUN_START", durationMS: LeadInMS, code: TrigRunStart,
			flipNS: runFlip, trigNS: trig, periodMS: -1})
		logData("RUN_START", 0, 0, nil, 0, TrigRunStart, runFlip, trig, -1)
		t0 := runFlip + msNS(LeadInMS)

		for s := 0; s < nSets; s++ {
			setNum := s + 1
			setT := t0 + msNS(setOffsetMS[s])
			instr := instrMS[s]
			r := showAt(setScreens[s], eventRow{event: "SET_INSTRUCTIONS", durationMS: SetInstrMS,
				code: TrigSetBase + byte(setNum), set: setNum, schedNS: setT})
			logData("SET_START", setNum, 0, nil, 0, r.code, r.flipNS, r.trigNS, -1)
			_ = exp.Data.Save()
			showAt(blank, eventRow{event: "SET_BLANK", durationMS: SetBlankMS,
				set: setNum, schedNS: setT + msNS(SetInstrMS)})

			// Random order of the 10 gestures, redrawn for each set.
			for p, gi := range rng.Perm(NGestures) {
				g := &gestures[gi]
				pos := p + 1
				gT := setT + msNS(SetInstrMS+SetBlankMS+p*gestureMS(instr))
				r := showAt(gestureScreens[gi], eventRow{event: "GESTURE_INSTRUCTIONS", durationMS: instr,
					code: TrigGestureBase + byte(g.num), set: setNum, gesturePos: pos, g: g, schedNS: gT})
				logData("GESTURE_START", setNum, pos, g, 0, r.code, r.flipNS, r.trigNS, -1)
				showAt(blank, eventRow{event: "GESTURE_BLANK", durationMS: GestureBlankMS,
					set: setNum, gesturePos: pos, g: g, schedNS: gT + msNS(instr)})

				// Execution window (10 s): GC disabled, cf. stimuli/stream.go.
				gcOld := debug.SetGCPercent(-1)
				var prevFlip uint64
				for rep := 1; rep <= NRepetitions; rep++ {
					sched := gT + msNS(instr+GestureBlankMS+(rep-1)*RepetitionMS)
					waitUntil(sched)
					_ = exp.Screen.Clear()
					_ = fix.Draw(exp.Screen)
					// The sound starts at the flip (≤ 1 audio buffer later), then the trigger.
					flipNS, err := beep.PlaySyncedWithFlip(exp.Screen)
					if err != nil {
						log.Printf("beep error (set %d, %s, rep %d): %v", setNum, g.id, rep, err)
					}
					code := TrigRepBase + byte(g.num)
					trigNS := sendTrigger(code)
					period := -1.0
					if prevFlip != 0 {
						period = float64(flipNS-prevFlip) / 1e6
					}
					prevFlip = flipNS
					evlog.write(eventRow{event: "REPETITION", durationMS: RepetitionMS, code: code,
						set: setNum, gesturePos: pos, g: g, repetition: rep,
						schedNS: sched, flipNS: flipNS, trigNS: trigNS, periodMS: period})
					logData("REPETITION", setNum, pos, g, rep, code, flipNS, trigNS, period)
				}
				debug.SetGCPercent(gcOld)
				_ = exp.Data.Save()
			}
		}

		// Run end: trigger 101 at the nominal end of the last set.
		r := showAt(blank, eventRow{event: "RUN_END", code: TrigRunEnd, schedNS: t0 + msNS(setOffsetMS[nSets])})
		logData("RUN_END", 0, 0, nil, 0, TrigRunEnd, r.flipNS, r.trigNS, -1)
		completed = true

		_ = exp.ShowInstructions("Finished, thank you!\n\nYou can relax now.")
		return control.EndLoop
	})

	if !completed {
		now := control.TicksNS()
		evlog.write(eventRow{event: "RUN_ABORTED", flipNS: now, periodMS: -1})
		logData("RUN_ABORTED", 0, 0, nil, 0, 0, now, 0, -1)
		log.Println("Run aborted before the end — partial data saved.")
	}
	if runErr != nil && !control.IsEndLoop(runErr) {
		exp.Fatal("experiment error: %v", runErr)
	}
}
