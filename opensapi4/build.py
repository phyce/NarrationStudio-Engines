#!/usr/bin/env python3
"""Build the SAPI 4 (OpenSAPI4) .nstudio bundle.

This pack has no Python. Its runner is a Go program (runner/) that speaks
Narration Studio's engine protocol with OpenSAPI4, and its voice data comes
from the OpenSAPI4 release runner/go.mod requires. The script:
  1. Builds runner/ for the target (OS, arch). It's pure Go, so every target
     builds on any host.
  2. Downloads the pinned OpenSAPI4 voice data, checks its size and SHA-256,
     and unpacks its microsoft and truvoice packs into voices/.
  3. Writes a filled-in manifest.json.
  4. Zips everything as opensapi4-<version>-<os>-<arch>-cpu.nstudio under dist/
     and writes a .metadata.json sidecar (copy of the manifest) next to it.

Usage:
    python build.py                          # host OS/arch
    python build.py --os linux --arch arm64  # another target
    python build.py --version 1.1.0          # stamp the manifest version

Requires: Python 3.10+, Go (the version in runner/go.mod), internet access.
"""

import argparse
import hashlib
import json
import os
import platform
import shutil
import subprocess
import sys
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath

ROOT = Path(__file__).parent.resolve()
DIST = ROOT / "dist"
DOWNLOADS = ROOT / "build" / "downloads"

ENGINE_ID = "opensapi4"
VARIANT = "cpu"
VARIANT_DESCRIPTION = "Open Source Implementation of the Microsoft Speech API v4"

OPENSAPI4_RELEASE = "v0.2.0"
VOICES_ASSET = f"opensapi4-voices-{OPENSAPI4_RELEASE}.zip"
VOICES_URL = f"https://github.com/phyce/OpenSAPI4/releases/download/{OPENSAPI4_RELEASE}/{VOICES_ASSET}"
VOICES_SIZE = 8215113
VOICES_SHA256 = "920e716afd33dbf5f02ecfe3e252c2e26be02244c0eb8a68bb654e0ef1e19554"
VOICE_PACKS = ("microsoft", "truvoice")

SUPPORTED_OS = ("windows", "linux", "darwin")


def detect_os() -> str:
    return {"darwin": "darwin", "linux": "linux", "windows": "windows"}.get(
        platform.system().lower(), platform.system().lower()
    )


def detect_arch() -> str:
    return {
        "amd64": "amd64", "x86_64": "amd64",
        "aarch64": "arm64", "arm64": "arm64",
    }.get(platform.machine().lower(), platform.machine().lower())


def runner_file_name(target_os: str) -> str:
    return "opensapi4-runner.exe" if target_os == "windows" else "opensapi4-runner"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--os", default=detect_os(), choices=SUPPORTED_OS)
    ap.add_argument("--arch", default=detect_arch())
    ap.add_argument("--version", default="")
    ap.add_argument("--keep-build", action="store_true",
                    help="Keep the staging folder; the voice data download is always kept.")
    ap.add_argument("--skip-deps", action="store_true",
                    help="Accepted for parity with the Python engines; this pack has no dependencies to install.")
    ap.add_argument("--engine-only", action="store_true",
                    help="Accepted for parity with the Python engines; this pack has no voice packs.")
    args = ap.parse_args()

    build = ROOT / "build" / f"{args.os}-{args.arch}"
    if not args.keep_build:
        shutil.rmtree(build, ignore_errors=True)
    build.mkdir(parents=True, exist_ok=True)
    DIST.mkdir(parents=True, exist_ok=True)

    build_runner(build, args.os, args.arch)
    unpack_voices(download_voices(), build / "voices")

    manifest = build_manifest(args)
    bundled_files = [
        path for path in sorted(build.rglob("*"))
        if path.is_file() and path != build / "manifest.json"
    ]
    manifest_size = len(json.dumps(manifest, indent=2))
    manifest["approxInstallSizeMB"] = max(
        1, round((sum(path.stat().st_size for path in bundled_files) + manifest_size) / (1024 * 1024))
    )
    (build / "manifest.json").write_text(json.dumps(manifest, indent=2), encoding="utf-8")

    bundle_name = f"{ENGINE_ID}-{manifest['version']}-{args.os}-{args.arch}-{VARIANT}"
    nstudio_path = DIST / f"{bundle_name}.nstudio"
    metadata_path = DIST / f"{bundle_name}.metadata.json"
    if nstudio_path.exists():
        nstudio_path.unlink()

    print(f"Zipping {nstudio_path}")
    with zipfile.ZipFile(nstudio_path, "w", zipfile.ZIP_DEFLATED, compresslevel=6) as zf:
        zf.write(build / "manifest.json", "manifest.json")
        for path in bundled_files:
            zf.write(path, path.relative_to(build).as_posix())

    metadata_path.write_text(json.dumps(manifest, indent=2), encoding="utf-8")

    size_mb = nstudio_path.stat().st_size / (1024 * 1024)
    print()
    print("Done.")
    print(f"  {nstudio_path}  ({size_mb:.1f} MB compressed, ~{manifest['approxInstallSizeMB']} MB on disk)")
    print(f"  {metadata_path}")


def build_runner(build: Path, target_os: str, target_arch: str):
    environment = dict(os.environ, GOOS=target_os, GOARCH=target_arch, CGO_ENABLED="0")
    run(
        ["go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w",
         "-o", str(build / runner_file_name(target_os)), "."],
        cwd=ROOT / "runner", env=environment,
    )


def download_voices() -> Path:
    DOWNLOADS.mkdir(parents=True, exist_ok=True)
    archive = DOWNLOADS / VOICES_ASSET
    if archive.exists() and is_expected_voices_archive(archive):
        return archive
    print(f"Downloading {VOICES_URL}")
    partial = archive.with_suffix(".part")
    urllib.request.urlretrieve(VOICES_URL, partial)
    if not is_expected_voices_archive(partial):
        partial.unlink()
        sys.exit(f"{VOICES_URL} isn't the expected file (size {VOICES_SIZE}, SHA-256 {VOICES_SHA256})")
    partial.replace(archive)
    return archive


def is_expected_voices_archive(archive: Path) -> bool:
    if archive.stat().st_size != VOICES_SIZE:
        return False
    digest = hashlib.sha256()
    with archive.open("rb") as archive_file:
        for block in iter(lambda: archive_file.read(1 << 20), b""):
            digest.update(block)
    return digest.hexdigest() == VOICES_SHA256


def unpack_voices(archive: Path, voices: Path):
    shutil.rmtree(voices, ignore_errors=True)
    print(f"Unpacking {archive.name}")
    with zipfile.ZipFile(archive) as zf:
        for entry in zf.infolist():
            if entry.is_dir():
                continue
            name = PurePosixPath(entry.filename)
            if len(name.parts) < 3 or name.parts[0] != "voices" or name.parts[1] not in VOICE_PACKS \
                    or ".." in name.parts or name.is_absolute():
                sys.exit(f"The voice data has an unexpected file {entry.filename!r}")
            destination = voices.joinpath(*name.parts[1:])
            destination.parent.mkdir(parents=True, exist_ok=True)
            with zf.open(entry) as source, destination.open("wb") as target:
                shutil.copyfileobj(source, target)
    for pack in VOICE_PACKS:
        if not (voices / pack).is_dir():
            sys.exit(f"The voice data has no {pack} pack")


def build_manifest(args) -> dict:
    template = json.loads((ROOT / "manifest.template.json").read_text(encoding="utf-8"))
    template["os"] = args.os
    template["arch"] = args.arch
    if args.version:
        template["version"] = args.version
    template["variant"] = VARIANT
    template["variantDescription"] = VARIANT_DESCRIPTION
    template["runtime"]["executable"] = runner_file_name(args.os)
    return template


def run(command, cwd=None, env=None):
    print("$ " + " ".join(str(part) for part in command))
    subprocess.run(command, check=True, cwd=cwd, env=env)


if __name__ == "__main__":
    main()
