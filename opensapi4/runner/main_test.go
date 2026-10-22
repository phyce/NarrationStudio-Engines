package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	opensapi4 "github.com/phyce/OpenSAPI4"
)

const testText = "Testing, one, two, three."

func testBundle(t *testing.T) (string, manifest) {
	t.Helper()
	voicesPath := os.Getenv("OPENSAPI4_VOICES")
	if voicesPath == "" {
		t.Skip("OPENSAPI4_VOICES is not set")
	}
	bundlePath := t.TempDir()
	rawManifest, err := os.ReadFile(filepath.Join("..", "manifest.template.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundlePath, "manifest.json"), rawManifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.CopyFS(filepath.Join(bundlePath, voicesFolderName), os.DirFS(voicesPath)); err != nil {
		t.Fatal(err)
	}
	var packManifest manifest
	if err := json.Unmarshal(rawManifest, &packManifest); err != nil {
		t.Fatal(err)
	}
	return bundlePath, packManifest
}

type session struct {
	exitCode int
	events   []event
	audio    []byte
}

func runRequests(t *testing.T, bundlePath string, requests ...request) session {
	t.Helper()
	var requestLines strings.Builder
	for _, outgoing := range requests {
		line, err := json.Marshal(outgoing)
		if err != nil {
			t.Fatal(err)
		}
		requestLines.Write(append(line, '\n'))
	}
	var audioOutput, eventOutput bytes.Buffer
	exitCode := run(bundlePath, strings.NewReader(requestLines.String()), &audioOutput, &eventOutput)
	var events []event
	for _, line := range strings.Split(strings.TrimSpace(eventOutput.String()), "\n") {
		var received event
		if err := json.Unmarshal([]byte(line), &received); err != nil {
			t.Fatalf("event line %q isn't JSON: %v", line, err)
		}
		events = append(events, received)
	}
	return session{exitCode: exitCode, events: events, audio: audioOutput.Bytes()}
}

func (finished session) eventFor(t *testing.T, requestID string) event {
	t.Helper()
	for _, received := range finished.events {
		if received.ID == requestID {
			return received
		}
	}
	t.Fatalf("no event for request %s in %+v", requestID, finished.events)
	return event{}
}

func expectedSpeech(t *testing.T, speechRequest opensapi4.Request) []byte {
	t.Helper()
	voices, err := opensapi4.Open(os.Getenv("OPENSAPI4_VOICES"))
	if err != nil {
		t.Fatal(err)
	}
	speech, err := voices.NewSpeech(speechRequest)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := speech.Speak(&audioSink{output: &output}); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func peak(pcm []byte) int {
	highest := 0
	for offset := 0; offset+1 < len(pcm); offset += 2 {
		sample := int(int16(binary.LittleEndian.Uint16(pcm[offset:])))
		highest = max(highest, sample, -sample)
	}
	return highest
}

func TestReadyLoadAndShutdown(t *testing.T) {
	bundlePath, packManifest := testBundle(t)
	finished := runRequests(t, bundlePath,
		request{ID: "1", Op: "load", Model: "opensapi4"},
		request{ID: "2", Op: "shutdown"},
		request{ID: "3", Op: "load", Model: "opensapi4"},
	)
	if finished.exitCode != 0 {
		t.Errorf("exit code %d", finished.exitCode)
	}
	ready := finished.events[0]
	if ready.Event != "ready" || ready.Runner != "opensapi4" || ready.Version != packManifest.Version {
		t.Errorf("first event %+v, want ready from opensapi4 %s", ready, packManifest.Version)
	}
	if loaded := finished.eventFor(t, "1"); loaded.Event != "done" || loaded.SampleRate != 22050 || loaded.Channels != 1 || loaded.Bits != 16 {
		t.Errorf("load answered %+v, want done at 22050 Hz, mono, 16 bits", loaded)
	}
	if finished.eventFor(t, "2").Event != "done" || len(finished.events) != 3 {
		t.Errorf("events %+v, want nothing after shutdown", finished.events)
	}
}

func TestEveryVoiceSpeaksInItsManifestFormat(t *testing.T) {
	bundlePath, packManifest := testBundle(t)
	voices := packManifest.Models[0].Voices
	requests := []request{{ID: "load", Op: "load", Model: "opensapi4"}}
	for _, voice := range voices {
		requests = append(requests, request{ID: voice.ID, Op: "synth", Model: "opensapi4", Voice: voice.ID, Text: testText})
	}
	finished := runRequests(t, bundlePath, requests...)
	if loaded := finished.eventFor(t, "load"); loaded.Event != "done" {
		t.Fatalf("load answered %+v", loaded)
	}

	audioOffset := 0
	for _, voice := range voices {
		spoken := finished.eventFor(t, voice.ID)
		if spoken.Event != "done" || spoken.SampleRate != voice.SampleRate || spoken.Bits != 16 {
			t.Errorf("%s answered %+v, want done at %d Hz", voice.ID, spoken, voice.SampleRate)
			continue
		}
		pcm := finished.audio[audioOffset : audioOffset+2*spoken.TotalSamples]
		audioOffset += 2 * spoken.TotalSamples
		if len(pcm) < voice.SampleRate || peak(pcm) < 300 {
			t.Errorf("%s spoke %d bytes peaking at %d", voice.ID, len(pcm), peak(pcm))
		}
	}
	if audioOffset != len(finished.audio) {
		t.Errorf("stdout carried %d bytes, the done events %d", len(finished.audio), audioOffset)
	}
}

func TestSpeechIsOpenSAPI4sOwn(t *testing.T) {
	bundlePath, _ := testBundle(t)
	finished := runRequests(t, bundlePath, request{ID: "1", Op: "synth", Model: "opensapi4", Voice: "trueman1", Text: testText + ` \Pau=200\ Done.`})
	expected := expectedSpeech(t, opensapi4.Request{Voice: "Adult Male #1, American English (TruVoice)", Text: testText + ` \Pau=200\ Done.`})
	if !bytes.Equal(finished.audio, expected) {
		t.Errorf("spoke %d bytes, OpenSAPI4 speaks %d", len(finished.audio), len(expected))
	}
}

func TestVoicesAreFoundByName(t *testing.T) {
	bundlePath, _ := testBundle(t)
	finished := runRequests(t, bundlePath, request{ID: "1", Op: "synth", Model: "opensapi4", Voice: "Mary (for Telephone)", Text: testText})
	if spoken := finished.eventFor(t, "1"); spoken.Event != "done" || spoken.SampleRate != 8000 {
		t.Errorf("answered %+v, want done at 8000 Hz", spoken)
	}
}

func TestPitchAndSpeedStayInTheVoicesRange(t *testing.T) {
	bundlePath, _ := testBundle(t)
	finished := runRequests(t, bundlePath, request{
		ID: "1", Op: "synth", Model: "opensapi4", Voice: "sam", Text: testText,
		Params: map[string]any{"pitch": 100000, "speed": 1},
	})
	highestPitch, lowestSpeed := 200, 30
	expected := expectedSpeech(t, opensapi4.Request{Voice: "Sam", Text: testText, Pitch: &highestPitch, Speed: &lowestSpeed})
	if !bytes.Equal(finished.audio, expected) {
		t.Errorf("spoke %d bytes, want Sam's highest pitch and lowest speed (%d bytes)", len(finished.audio), len(expected))
	}
}

func TestRefusedRequestsWriteNoAudio(t *testing.T) {
	bundlePath, _ := testBundle(t)
	finished := runRequests(t, bundlePath,
		request{ID: "voice", Op: "synth", Model: "opensapi4", Voice: "nobody", Text: testText},
		request{ID: "text", Op: "synth", Model: "opensapi4", Voice: "sam", Text: ""},
		request{ID: "model", Op: "load", Model: "microsoft"},
		request{ID: "op", Op: "prepare"},
	)
	for requestID, expectedCode := range map[string]string{
		"voice": "voice_not_found",
		"text":  "bad_request",
		"model": "model_not_found",
		"op":    "unknown_op",
	} {
		if refused := finished.eventFor(t, requestID); refused.Event != "error" || refused.Code != expectedCode {
			t.Errorf("%s answered %+v, want error %s", requestID, refused, expectedCode)
		}
	}
	if len(finished.audio) != 0 {
		t.Errorf("refused requests wrote %d bytes of audio", len(finished.audio))
	}
}

func TestMissingVoiceDataFailsTheLoad(t *testing.T) {
	bundlePath, _ := testBundle(t)
	if err := os.RemoveAll(filepath.Join(bundlePath, voicesFolderName, opensapi4.TruVoiceFolder)); err != nil {
		t.Fatal(err)
	}
	finished := runRequests(t, bundlePath, request{ID: "1", Op: "load", Model: "opensapi4"})
	if loaded := finished.eventFor(t, "1"); loaded.Event != "error" || loaded.Code != "bad_bundle" || !strings.Contains(loaded.Error, "reinstall") {
		t.Errorf("load answered %+v, want a bad_bundle error", loaded)
	}
}

func TestMissingManifestStopsTheRunner(t *testing.T) {
	var eventOutput bytes.Buffer
	if exitCode := run(t.TempDir(), strings.NewReader(""), &bytes.Buffer{}, &eventOutput); exitCode == 0 {
		t.Error("the runner started without a manifest")
	}
	if !strings.Contains(eventOutput.String(), "manifest") {
		t.Errorf("stderr %q doesn't say what's wrong", eventOutput.String())
	}
}
