package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"

	opensapi4 "github.com/phyce/OpenSAPI4"
)

const voicesFolderName = "voices"

type manifest struct {
	ID      string          `json:"id"`
	Version string          `json:"version"`
	Models  []manifestModel `json:"models"`
}

type manifestModel struct {
	ID     string          `json:"id"`
	Voices []manifestVoice `json:"voices"`
}

type manifestVoice struct {
	ID         string                     `json:"id"`
	Name       string                     `json:"name"`
	SampleRate int                        `json:"sampleRate"`
	Ranges     map[string]opensapi4.Range `json:"ranges"`
}

type request struct {
	ID     string         `json:"id"`
	Op     string         `json:"op"`
	Model  string         `json:"model"`
	Voice  string         `json:"voice"`
	Text   string         `json:"text"`
	Params map[string]any `json:"params"`
}

type event struct {
	ID           string `json:"id,omitempty"`
	Event        string `json:"event"`
	Runner       string `json:"runner,omitempty"`
	Version      string `json:"version,omitempty"`
	Error        string `json:"error,omitempty"`
	Code         string `json:"code,omitempty"`
	SampleRate   int    `json:"sampleRate,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	Bits         int    `json:"bits,omitempty"`
	TotalSamples int    `json:"totalSamples,omitempty"`
}

type requestFailure struct {
	code    string
	message string
}

func (failure *requestFailure) Error() string {
	return failure.message
}

func failure(code, format string, arguments ...any) error {
	return &requestFailure{code: code, message: fmt.Sprintf(format, arguments...)}
}

type runner struct {
	bundlePath   string
	manifest     manifest
	voices       *opensapi4.Voices
	listedVoices map[string]opensapi4.Voice
	audioOutput  io.Writer
	eventOutput  io.Writer
}

func main() {
	bundlePath := flag.String("bundle-dir", ".", "the folder of the installed pack")
	flag.Parse()
	os.Exit(run(*bundlePath, os.Stdin, os.Stdout, os.Stderr))
}

func run(bundlePath string, requests io.Reader, audioOutput, eventOutput io.Writer) int {
	packManifest, err := readManifest(bundlePath)
	if err != nil {
		fmt.Fprintf(eventOutput, "[opensapi4-runner] %v\n", err)
		return 1
	}
	sapi4Runner := &runner{bundlePath: bundlePath, manifest: packManifest, audioOutput: audioOutput, eventOutput: eventOutput}
	sapi4Runner.emit(event{Event: "ready", Runner: packManifest.ID, Version: packManifest.Version})

	scanner := bufio.NewScanner(requests)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		var incoming request
		if err := json.Unmarshal(scanner.Bytes(), &incoming); err != nil {
			fmt.Fprintf(eventOutput, "[opensapi4-runner] unreadable request: %v\n", err)
			continue
		}
		if incoming.Op == "shutdown" {
			sapi4Runner.emit(event{ID: incoming.ID, Event: "done"})
			return 0
		}
		sapi4Runner.handle(incoming)
	}
	return 0
}

func readManifest(bundlePath string) (manifest, error) {
	rawManifest, err := os.ReadFile(filepath.Join(bundlePath, "manifest.json"))
	if err != nil {
		return manifest{}, fmt.Errorf("read the pack's manifest: %w", err)
	}
	var packManifest manifest
	if err := json.Unmarshal(rawManifest, &packManifest); err != nil {
		return manifest{}, fmt.Errorf("read the pack's manifest: %w", err)
	}
	return packManifest, nil
}

func (sapi4Runner *runner) emit(outgoing event) {
	line, _ := json.Marshal(outgoing)
	sapi4Runner.eventOutput.Write(append(line, '\n'))
}

func (sapi4Runner *runner) handle(incoming request) {
	var err error
	switch incoming.Op {
	case "load":
		err = sapi4Runner.load(incoming)
	case "synth":
		err = sapi4Runner.synthesize(incoming)
	case "unload":
		sapi4Runner.emit(event{ID: incoming.ID, Event: "done"})
	default:
		err = failure("unknown_op", "unknown op: %s", incoming.Op)
	}
	if err == nil {
		return
	}
	code := "runtime_error"
	var knownFailure *requestFailure
	if errors.As(err, &knownFailure) {
		code = knownFailure.code
	}
	sapi4Runner.emit(event{ID: incoming.ID, Event: "error", Error: err.Error(), Code: code})
}

func (sapi4Runner *runner) openVoices() error {
	if sapi4Runner.voices != nil {
		return nil
	}
	voicesPath := filepath.Join(sapi4Runner.bundlePath, voicesFolderName)
	voices, err := opensapi4.Open(voicesPath)
	if err != nil {
		return failure("bad_bundle", "open the voice data in %s: %v; reinstall the pack", voicesPath, err)
	}
	list, err := voices.List()
	if err != nil {
		return failure("bad_bundle", "read the voice data in %s: %v; reinstall the pack", voicesPath, err)
	}
	listedVoices := make(map[string]opensapi4.Voice, len(list))
	for _, listedVoice := range list {
		listedVoices[listedVoice.Name] = listedVoice
	}
	sapi4Runner.voices, sapi4Runner.listedVoices = voices, listedVoices
	return nil
}

func (sapi4Runner *runner) findModel(modelID string) (manifestModel, bool) {
	for _, model := range sapi4Runner.manifest.Models {
		if model.ID == modelID {
			return model, true
		}
	}
	return manifestModel{}, false
}

func (sapi4Runner *runner) load(incoming request) error {
	model, found := sapi4Runner.findModel(incoming.Model)
	if !found {
		return failure("model_not_found", "this pack has no model %q", incoming.Model)
	}
	if len(model.Voices) == 0 {
		return failure("bad_bundle", "model %s has no voices", model.ID)
	}
	if err := sapi4Runner.openVoices(); err != nil {
		return err
	}
	modelSampleRate := 0
	for _, voice := range model.Voices {
		listedVoice, found := sapi4Runner.listedVoices[voice.Name]
		if !found {
			return failure("bad_bundle", "the voice data has no voice %q (%s); reinstall the pack", voice.Name, voice.ID)
		}
		if modelSampleRate == 0 {
			modelSampleRate = listedVoice.SampleRate
		}
		expectedSampleRate := voice.SampleRate
		if expectedSampleRate == 0 {
			expectedSampleRate = modelSampleRate
		}
		if listedVoice.SampleRate != expectedSampleRate {
			return failure("bad_bundle", "voice %s speaks at %d Hz, not the %d Hz its manifest gives", voice.ID, listedVoice.SampleRate, expectedSampleRate)
		}
		if voice.Ranges["pitch"] != listedVoice.Pitch || voice.Ranges["speed"] != listedVoice.Speed {
			return failure("bad_bundle", "voice %s has pitch %+v and speed %+v, not the ranges its manifest gives", voice.ID, listedVoice.Pitch, listedVoice.Speed)
		}
	}
	sapi4Runner.emit(event{ID: incoming.ID, Event: "done", SampleRate: modelSampleRate, Channels: 1, Bits: 16})
	return nil
}

func (sapi4Runner *runner) findVoice(modelID, voiceIdentifier string) (manifestVoice, bool) {
	models := sapi4Runner.manifest.Models
	if model, found := sapi4Runner.findModel(modelID); found {
		models = []manifestModel{model}
	}
	for _, model := range models {
		for _, voice := range model.Voices {
			if voice.ID == voiceIdentifier {
				return voice, true
			}
		}
	}
	for _, model := range models {
		for _, voice := range model.Voices {
			if voice.Name == voiceIdentifier {
				return voice, true
			}
		}
	}
	return manifestVoice{}, false
}

func (sapi4Runner *runner) synthesize(incoming request) error {
	if err := sapi4Runner.openVoices(); err != nil {
		return err
	}
	voice, found := sapi4Runner.findVoice(incoming.Model, incoming.Voice)
	if !found {
		return failure("voice_not_found", "voice %q not found", incoming.Voice)
	}
	listedVoice, found := sapi4Runner.listedVoices[voice.Name]
	if !found {
		return failure("bad_bundle", "the voice data has no voice %q (%s); reinstall the pack", voice.Name, voice.ID)
	}

	speechRequest := opensapi4.Request{Voice: voice.Name, Text: incoming.Text}
	applyVoiceSettings(&speechRequest, listedVoice, incoming.Params)
	speech, err := sapi4Runner.voices.NewSpeech(speechRequest)
	var refusedRequest *opensapi4.RequestError
	if errors.As(err, &refusedRequest) {
		return failure("bad_request", "%v", err)
	}
	if err != nil {
		return err
	}

	sink := &audioSink{output: sapi4Runner.audioOutput}
	if err := speech.Speak(sink); err != nil {
		return err
	}
	format := speech.Format()
	sapi4Runner.emit(event{
		ID:           incoming.ID,
		Event:        "done",
		SampleRate:   format.SampleRate,
		Channels:     1,
		Bits:         format.Bits,
		TotalSamples: sink.writtenBytes / 2,
	})
	return nil
}

var styleTags = map[string]string{
	"whisper":  `\Chr="Whisper"\`,
	"monotone": `\Chr="Monotone"\`,
}

func applyVoiceSettings(speechRequest *opensapi4.Request, listedVoice opensapi4.Voice, params map[string]any) {
	if percent, given := params["pitch"].(float64); given {
		pitch := relativeToVoice(listedVoice.Pitch, percent)
		speechRequest.Pitch = &pitch
	}
	if percent, given := params["speed"].(float64); given {
		speed := relativeToVoice(listedVoice.Speed, percent)
		speechRequest.Speed = &speed
	}
	if style, given := params["style"].(string); given {
		speechRequest.Text = styleTags[style] + speechRequest.Text
	}
}

func relativeToVoice(voiceRange opensapi4.Range, percent float64) int {
	value := math.Round(float64(voiceRange.Default) * percent / 100)
	return int(min(max(value, float64(voiceRange.Minimum)), float64(voiceRange.Maximum)))
}

type audioSink struct {
	output       io.Writer
	writtenBytes int
}

func (sink *audioSink) Audio(data []byte) (int, error) {
	written, err := sink.output.Write(data)
	sink.writtenBytes += written
	return written, err
}

func (sink *audioSink) Event(opensapi4.Event) error {
	return nil
}
