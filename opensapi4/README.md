# SAPI 4 engine pack

Bundle producer for a [Narration Studio](https://github.com/phyce/Narration-Studio) engine pack with the Microsoft
SAPI 4 voices: Sam, Mike, Mary, and TruVoice using [OpenSAPI4](https://github.com/phyce/OpenSAPI4).

## What this produces

```
dist/
├── opensapi4-<version>-<os>-<arch>-cpu.nstudio        # ZIP: manifest.json, opensapi4-runner[.exe], voices/
└── opensapi4-<version>-<os>-<arch>-cpu.metadata.json  # Catalog sidecar (copy of manifest.json)
```

The Build release workflow attaches both files to a draft release `opensapi4-v<version>` (see the
top-level README); publishing it adds them to the catalog. The only variant is `cpu`.

## Build prerequisites

- Python 3.10+ on PATH (to drive the build)
- Go, the version in `runner/go.mod`
- Internet access (Go modules + the OpenSAPI4 voice data, ~8 MB)

## Building

```powershell
.\build-windows-cpu.ps1                  # Windows amd64 bundle
.\build-windows-cpu.ps1 --version 1.1.0  # extra args pass through to build.py
python build.py --os linux --arch arm64  # any other target
```

The runner is pure Go, so every target builds on any host. `build.py`:

1. builds `runner/` for the target (`CGO_ENABLED=0`, `-trimpath`);
2. downloads OpenSAPI4's `opensapi4-voices-<release>.zip`, checks its size and SHA-256
   (`OPENSAPI4_RELEASE`, `VOICES_SIZE`, `VOICES_SHA256`), keeps it in `build/downloads/`, and
   unpacks its `microsoft` and `truvoice` packs into `voices/`;
3. fills in `manifest.template.json` (os, arch, version, variant, `runtime.executable`);
4. zips everything into `dist/`.

## Voices

The engine and model are both `opensapi4`. The voice IDs are those of Narration Studio's old
built-in SAPI 4 engine (`sam`, `mary`, `maryphone`, `robo1`…`robo6`, `whisperfemale`…; TruVoice
`trueman1`…`trueman8`, `truefemale1`, `truefemale2`), A voice's SAPI 4 mode name (its `name` in the
manifest) also works as its ID.

The voices don't all speak at the same rate: 22050 Hz, the telephone voices 8000 Hz, TruVoice
11025 Hz. Every manifest voice carries its `sampleRate`, so the host opens each stream in the right
format, and `op:load` checks every voice's rate against the voice data — a mismatch fails the
install's test run instead of playing at the wrong speed.

`synth` reads two optional `params`: `pitch` (Hz) and `speed` (words per minute), each clamped
to the voice's range. Narration Studio doesn't send them yet. Texts are spoken tagged, so SAPI 4
tags such as `\Pau=500\` work.

## Updating OpenSAPI4

1. `go get github.com/phyce/OpenSAPI4@<tag>` in `runner/`.
2. If the release has new voice data, update `OPENSAPI4_RELEASE`, `VOICES_SIZE` and
   `VOICES_SHA256` in `build.py` (the zip's sum depends only on the data, so a release with the
   same data keeps it), and the manifest's voices if any were added.
3. Bump the version (`--version`, or `manifest.template.json`).

## Testing

```powershell
cd runner
$env:OPENSAPI4_VOICES = "<an unpacked voices folder, e.g. ..\build\windows-amd64\voices>"
go test ./...
```

The tests skip without `OPENSAPI4_VOICES`. They run the runner against a bundle made from that
folder and `manifest.template.json`, and check the speech is OpenSAPI4's own, byte for byte. They
don't check the zip `build.py` makes, so also install the built `.nstudio` in Narration Studio
with **Import**: that runs the real test run on the packed files.
