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

Every source is pinned (the interpreter, package versions in a committed, hash-pinned
`requirements-<os>-<arch>-<device>.lock` or `go.sum`, upstream commits, checksums of downloaded
model files), so a clean checkout builds the same pack on any computer. Each engine's README has
the details.

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
     it once people use the pack. `name`, `version` (the pack's own version, `1.0.0`),
     `engineVersion` (optional: the version of the engine it wraps, shown as its version) and
     `variant` (such as `cpu`).
   - `description` and `links` (optional): `website` and `repository` of the project the pack
     wraps. Narration Studio shows them under the description before install and behind the info
     button on the pack's card after it. Only `https://` and `http://` addresses are shown.
   - `schemaVersion` and `protocol`: both `1`. `os` and `arch`: where it runs (`windows`, `amd64`).
   - `minAppVersion`: the oldest Narration Studio the pack works with. Every pack here sets it,
     `0.18.0` at least (the first version with voice settings); raise it when a pack relies on
     something Narration Studio added later. Older versions show the pack as needing an update
     and won't install it (versions up to 0.17.0 don't read it).
   - `runtime`: `python` and `entrypoint` for a Python runner, or `executable` for a program of
     its own, plus `env` and `bootTimeoutSec`.
   - `models`, each with its `voices` (`id`, `name`, `gender`). A voice-cloning model has no
     voices of its own and names a short reference clip in `testVoice` instead, for the test run
     at install.
   - `settings` (optional): what users can change about a voice, such as pitch or speed. Each has
     an `id`, `name`, optional `description` and a `type`: `number` (`minimum`, `maximum`, `step`,
     `unit`, `default`) or `choice` (`choices` of `value` and `name`, `default`). People set them
     for the whole engine, for one model or for one character, and the runner gets the result in
     each `synth` request's `params`, numbers clamped to the range and only listed choices. A
     setting nobody changed isn't sent, so the runner uses its own default. A number relative to
     each voice (a percentage of its own pitch) names the voice's unit in `voiceUnit`, and every
     voice then lists `ranges` for it (`default`, `minimum`, `maximum` in that unit).
     `opensapi4/manifest.template.json` has both kinds.

   `app/tts/engine/python/structs.go` in Narration Studio defines every field.
3. **Zip it** with `manifest.json` at the root of the zip, and give the file the `.nstudio`
   extension. Copying one of the engine folders here and adapting its `build.py` does this for you.
4. **Import it** with **Voice Packs → Import pack**. Narration Studio warns before installing a pack
   from your own files: a pack's runner runs with the app's permissions.
