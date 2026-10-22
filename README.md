# NarrationStudio-Engines

Engine packs for [Narration Studio](https://github.com/phyce/Narration-Studio). A pack
(`.nstudio`) adds a local text-to-speech engine: Narration Studio downloads published packs on its
Voice Packs page, and installs one you built yourself with **Import pack**.

Each folder here builds one engine's packs. Most run a Python runner on a bundled interpreter.

## Building a pack yourself

You need Python 3.10 or newer and an internet connection. Packs that install from git need git,
and `opensapi4/` needs Go (the version in its `runner/go.mod`).

Run the engine's wrapper script inside the engine folder:

```powershell
.\build-windows-cpu.ps1
```

or its `build.py` for other options, such as the variant, the target `--os` / `--arch` or
`--version` (`python kokoro/build.py --help`). A Python pack has to be built on the platform it's
for, because pip downloads native wheels for the computer it runs on. `opensapi4/` cross-compiles,
so any computer builds any target.

The build writes two files to the engine's `dist/` folder:

```
<id>-<version>-<os>-<arch>-<variant>.nstudio        # the pack
<id>-<version>-<os>-<arch>-<variant>.metadata.json  # a copy of its manifest.json
```

Install the `.nstudio` with **Voice Packs → Import pack**. Narration Studio loads the pack and
speaks a test sentence with it before it switches over.

Every source is pinned (the interpreter, package versions in a committed `requirements.lock` or
`go.sum`, upstream commits, checksums of downloaded model files), so a clean checkout builds the
same pack on any computer. Each engine's README has the details.

## Making your own pack

A pack is a zip file with the `.nstudio` extension. `manifest.json` sits at its root, next to a
runner and whatever the runner needs:

```
manifest.json
runner.py          # or a program of its own
python/            # a bundled interpreter, such as python-build-standalone
site-packages/     # the runner's dependencies (pip install --target)
models/            # weights, voices, ...
```

1. **Write the runner.** Narration Studio starts it with `--bundle-dir <pack folder>` and talks
   to it over three pipes: JSON requests on stdin (`load`, `synth`, `shutdown`), raw 16-bit PCM on
   stdout, and JSON events on stderr. The runner announces itself with a `ready` event and answers
   every request with `done` (`load`'s carries the sample rate) or `error`. The full contract is
   `docs/python-engine-protocol.md` in Narration Studio; `kokoro/runner.py` is the shortest
   example here.
2. **Write `manifest.json`.** Start from `kokoro/manifest.template.json`:
   - `id`: lowercase letters, digits and dashes. Profiles store voices as `<id>:<model>`, so keep
     it once people use the pack. `name`, `version` (`1.0.0`) and `variant` (such as `cpu`).
   - `schemaVersion` and `protocol`: both `1`. `os` and `arch`: where it runs (`windows`, `amd64`).
   - `runtime`: `python` and `entrypoint` for a Python runner, or `executable` for a program of
     its own, plus `env` and `bootTimeoutSec`.
   - `models`, each with its `voices` (`id`, `name`, `gender`). A voice-cloning model has no
     voices of its own and names a short reference clip in `testVoice` instead, for the test run
     at install.

   `app/tts/engine/python/structs.go` in Narration Studio defines every field.
3. **Zip it** with `manifest.json` at the root of the zip, and give the file the `.nstudio`
   extension. Copying one of the engine folders here and adapting its `build.py` does this for you.
4. **Import it** with **Voice Packs → Import pack**. Narration Studio warns before installing a pack
   from your own files: a pack's runner runs with the app's permissions.
