# FacialGestures — replication of Tuomaala et al. (2025)

> Tuomaala, S., Autti, S., Cotroneo, S.F., Lioumis, P., Renvall, H., Liljeström, M.
> (2025). *Automated speech artefact removal from MEG data utilizing facial gestures
> and mutual information.* Imaging Neuroscience, 3, imag_a_00545.
> doi:10.1162/imag_a_00545 — code: https://github.com/BioMag/speech_artefact_removal

This is a preliminary task run **before** a naming task. It records clean examples
of facial muscle artefacts (with simultaneous EMG), which are then used to remove
speech artefacts automatically with ICA and mutual information. The run takes place
in the NeuroSpin MEG.

## Paradigm (as implemented)

Three nested levels. The paper's duration is **830 s** (5 sets, 13 min 50 s). **By
default we run a shortened version: 458 s (7 min 38 s)**, i.e. 3 sets, with the
gesture instructions shortened to 3 s from set 2 on. Add a 1 s lead-in (see
deviations below). Paper timing: `--sets 5 --later-instr-ms 5000`.

| Level | Content | Duration |
|---|---|---|
| Set (×3 by default; ×5 in the paper) | set instructions 5 s → blank 1 s → 10 gestures | 166 s (set 1), 146 s (later sets) |
| Gesture (×10 per set, new random order each set) | instructions + schematic 5 s (set 1) / 3 s (later sets) → blank 1 s → 5 repetitions | 16 s / 14 s |
| Repetition (×5 per gesture) | 10 ms beep ("go") + 1990 ms execution window, fixation cross | 2 s |

By default that gives 15 repetitions per gesture and 150 gesture events per
participant (paper: 25 and 250). The
gesture name (and its schematic) appears only on the gesture instruction screen.
During repetitions only a central fixation cross is shown.

### The 10 gestures (O'Dwyer et al., 1981)

| Code | Muscle | On-screen instruction | Schematic view (FACS analogue) |
|---|---|---|---|
| G1 | Levator labii superioris | Raise your upper lip on one side only (sneer) | frontal (AU10, unilateral) |
| G2 | Zygomaticus major | Smile broadly | frontal (AU12+25) |
| G3 | Buccinator | Puff out your cheeks, lips closed | frontal (AU34) |
| G4 | Risorius | Smile broadly with your lips closed | frontal (AU12/AU20) |
| G5 | Orbicularis oris superioris | Press your upper lip against your upper front teeth | mid-sagittal section |
| G6 | Orbicularis oris inferioris | Press your lower lip against your lower front teeth | mid-sagittal section |
| G7 | Depressor labii inferioris | Pull your lower lip down, keeping your jaw closed | frontal (AU16+25) |
| G8 | Depressor anguli oris | Pull the corners of your mouth down | frontal (AU15) |
| G9 | Mentalis | Push your lower lip up and out, wrinkling your chin | frontal (AU17) |
| G10 | Genioglossus | Press your tongue against the roof of your mouth | mid-sagittal section |

Each schematic shows two panels, **at rest → gesture**. The target muscle is
highlighted in orange and blue arrows show the direction of movement. The drawings
are generated in code (`schematics.go`). Muscle locations are approximate, based on
standard facial anatomy. The frontal views follow the appearance changes described
for the matching FACS action units (Ekman & Friesen, 1978).

### Beep

10 ms, 1000 Hz, 2 ms raised-cosine ramps. It is generated in memory as a 16-bit PCM
mono WAV, so there is no asset file. It starts on a screen flip
(`PlaySyncedWithFlip`), and playback does not block the loop.

## Triggers

We follow the conventions of `examples/MindSentencesExtension`:

- **Device**: parallel port `/dev/parport1` (`--parport`). It uses the ppdev code
  copied verbatim from MindSentences (`parport_linux.go`, `parport_other.go`), not
  `triggers.ParallelPort`, whose ioctl numbers are wrong.
- If the port cannot be opened: a clear WARNING, a null device, and the run
  continues. `--trigger none` disables triggers explicitly.
- A single entry point: `sendTrigger(code)`. The pulse blocks: lines go high, stay
  high for `--pulse-ms` (5 ms), then return to 0.
- **Timing**: the trigger is sent **right after the flip** that starts the beep. The
  flip timestamp and the trigger-rise timestamp are both logged.

| Code | Event |
|---|---|
| 1..10 | repetition onset (= gesture number) |
| 11..20 | gesture instruction onset (10 + gesture number) |
| 21..23 | set start (20 + set number; 21..25 with `--sets 5`) |
| 100 | run start |
| 101 | run end |

**Critical timing point.** The event of interest is the beep, not the flip. The
trigger→sound delay depends on the audio chain in the MEG room. It must be measured
there (microphone or sensor on a MISC channel) and subtracted at analysis. On the
goxpyriment test bench a TTL→audio lag of about 100 ms (SD 6 ms) was reported. That
figure was not found in the repository docs (`docs/TimingTests.md` gives about
12 ms of audio-pipeline latency), so do not reuse it as is.

## Outputs

Both files go to the experiment's own `data/` folder (created automatically,
git-ignored): next to the binary, or next to `main.go` under `go run`;
`--data-dir` overrides it, as in MindSentences:

1. `FacialGestures_sub-XXX_date-….csv`, the goxpyriment data file with one row per
   repetition, per gesture block (`GESTURE_START`), per set (`SET_START`), plus
   `RUN_START`, `RUN_END` and `RUN_ABORTED`. Columns: `event, set,
   gesture_position, gesture_id, muscle, repetition, trigger_code, t_onset_ns,
   t_trigger_ns, onset_run_s, period_ms`.
2. `FacialGestures_sub-XXX_date-YYYYMMDD-HHMM_events.tsv` (same base name as the
   .csv), the full BIDS-style event log. It has one row per screen or event,
   including the blank screens, and is flushed after every row. There is a single
   run per participant, so there is no run number; a relaunch gets a new timestamp.
   Columns:

   | column | meaning |
   |---|---|
   | `onset`, `duration` | s, relative to the run-start flip (trigger 100); nominal duration |
   | `trial_type`, `value` | event label; trigger code (`n/a` if none) |
   | `event_index`, `subject_id`, `seed` | identifiers (seed reproduces the gesture orders) |
   | `set`, `gesture_position`, `gesture_id`, `gesture_number`, `muscle`, `gesture_label`, `repetition` | stimulus |
   | `scheduled_onset_s`, `onset_error_ms` | nominal onset and flip − nominal |
   | `period_ms` | realised period since the previous repetition of the same gesture |
   | `t_flip_ns`, `t_trigger_ns`, `trigger_minus_flip_ms` | SDL clock timestamps |

## Deviations from the brief / the paper

- **Absolute schedule.** The 5 ms pulse is not subtracted from the 1990 ms by hand.
  Every event is instead scheduled at `t0 + nominal offset` and shown on the nearest
  flip, so the 2000 ms period and the total duration hold without drift. Measured on
  the development machine (60 Hz, windowed): periods 1996–2003 ms, onset errors
  within ±8 ms.
- **1 s lead-in** between trigger 100 and the first set screen, so codes 100 and 21
  don't run together on STI. The run lasts 831 s from trigger 100; trigger 101 is at
  the nominal end of the last set (t0 + 458 s by default).
- **Device**: parallel port rather than MEGTTLBox, and run codes **100/101** rather
  than 250/251, to match MindSentences (decided with the experimenter).
- **Shorter default (3 sets, 7 min 38 s instead of 5 sets, 13 min 50 s)**, chosen
  because the full version was too long for our sessions. From set 2 on, the gesture
  instruction screen lasts 3 s instead of 5 s, since the participant knows the
  gestures by then. Beep timing (10 ms + 1990 ms) and the 5 repetitions per gesture
  are unchanged. This gives 15 repetitions per gesture instead of 25. The paper did
  not test shorter gesture recordings ("Future studies could address the optimal
  EMG placement and gesture task duration and structure"). With less gesture data,
  MIC-ICA-GN may lose some of its robustness advantage over MIC-ICA-N
  (called MIC-ICA-G in the paper's Fig. 1). Paper
  timing: `--sets 5 --later-instr-ms 5000`.
- **Language**: on-screen text is in **English**, following a later request (the
  brief originally asked for French).
- **Schematics** under each gesture instruction (not in the paper), also a later
  request.
- **Files**: `main.go` + `schematics.go` + the two parport files. A single file was
  not possible, because the ppdev code must be build-tagged Linux-only to keep the
  example cross-compilable.
- `exp.FittedTextBox` does not exist. Text uses `stimuli.TextBox` wrapped at 80% of
  the screen width, and the schematic is sized to the remaining height.
- "Blank screen" is implemented as an empty black screen (black background).
- Extra experimenter screens (hardware status) and a participant instruction screen
  come before trigger 100, and SPACE advances them. They are not counted in the task duration.
