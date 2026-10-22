#!/usr/bin/env python3
"""Build catalog.json from the repository's GitHub releases.

Narration Studio reads one catalog.json instead of listing releases itself. This
script lists every published release, pairs each <id>-<version>-<os>-<arch>-<variant>.nstudio
asset with its .metadata.json sidecar, checks both against the release tag
(<id>-v<version>), and writes one entry per bundle:

    {"manifest": {...sidecar...}, "url": ..., "size": ..., "sha256": ...,
     "notes": <release description>, "publishedAt": ...}

Run by .github/workflows/update-catalog.yml on every release change. Problems
(missing sidecar, wrong names, manifest not matching the tag) leave that bundle
out, are printed as GitHub error annotations, and make the script exit 1 after
the catalog is written — so one broken release never hides the others. A
failure to list releases exits 2 without writing anything, so a GitHub outage
can't publish an empty catalog.

Usage:
    python scripts/generate_catalog.py --repository phyce/NarrationStudio-Engines --output catalog.json
    python scripts/generate_catalog.py --releases-file releases.json --output catalog.json   # offline

Uses GH_TOKEN / GITHUB_TOKEN when set (anonymous requests are limited to 60 per hour).
Standard library only.
"""

import argparse
import hashlib
import json
import os
import re
import sys
import urllib.request
from pathlib import Path

API_ROOT = "https://api.github.com"
TAG_PATTERN = re.compile(r"^(?P<id>[a-z0-9][a-z0-9-]*)-v(?P<version>\d+\.\d+\.\d+)$")
VERSION_PATTERN = re.compile(r"^\d+\.\d+\.\d+$")
REQUIRED_MANIFEST_FIELDS = ("schemaVersion", "protocol", "id", "name", "version", "os", "arch", "variant")

problems = []


def report_problem(release_tag, message):
    problems.append(f"{release_tag}: {message}")
    if os.environ.get("GITHUB_ACTIONS"):
        print(f"::error title=Release {release_tag}::{message}")
    else:
        print(f"error: {release_tag}: {message}", file=sys.stderr)


def open_url(url, accept=None):
    headers = {"User-Agent": "NarrationStudio-catalog"}
    if accept:
        headers["Accept"] = accept
    token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
    if token and url.startswith(API_ROOT):
        headers["Authorization"] = f"Bearer {token}"
    return urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=60)


def list_releases(repository):
    releases = []
    page = 1
    while True:
        url = f"{API_ROOT}/repos/{repository}/releases?per_page=100&page={page}"
        with open_url(url, accept="application/vnd.github+json") as response:
            batch = json.load(response)
        if not batch:
            return releases
        releases.extend(batch)
        page += 1


def asset_sha256(asset):
    digest = asset.get("digest") or ""
    if digest.startswith("sha256:"):
        return digest[len("sha256:"):]
    print(f"{asset['name']}: GitHub has no digest, hashing the download", file=sys.stderr)
    sha256 = hashlib.sha256()
    with open_url(asset["browser_download_url"]) as response:
        for block in iter(lambda: response.read(1024 * 1024), b""):
            sha256.update(block)
    return sha256.hexdigest()


def manifest_problem(manifest, tag_id, tag_version, bundle_name):
    missing = [field for field in REQUIRED_MANIFEST_FIELDS if manifest.get(field) in (None, "")]
    if missing:
        return f"{bundle_name}: manifest is missing {', '.join(missing)}"
    if not isinstance(manifest["protocol"], int):
        return f"{bundle_name}: manifest protocol must be a whole number, got {manifest['protocol']!r}"
    if not VERSION_PATTERN.match(str(manifest["version"])):
        return f"{bundle_name}: manifest version {manifest['version']!r} is not <major>.<minor>.<patch>"
    if manifest["id"] != tag_id or manifest["version"] != tag_version:
        return (f"{bundle_name}: manifest says {manifest['id']} {manifest['version']}, "
                f"but the tag says {tag_id} {tag_version}")
    expected_name = (f"{manifest['id']}-{manifest['version']}-{manifest['os']}-"
                     f"{manifest['arch']}-{manifest['variant']}.nstudio")
    if bundle_name != expected_name:
        return f"{bundle_name}: expected the file to be named {expected_name}"
    return None


def catalog_entries(release):
    tag = release.get("tag_name") or "(untagged)"
    tag_match = TAG_PATTERN.match(tag)
    if not tag_match:
        report_problem(tag, "tag must look like <id>-v<major>.<minor>.<patch>, e.g. kokoro-v1.0.0")
        return []

    assets_by_name = {asset["name"]: asset for asset in release.get("assets") or []}
    bundle_names = sorted(name for name in assets_by_name if name.endswith(".nstudio"))
    if not bundle_names:
        report_problem(tag, "release has no .nstudio files attached")
        return []

    for name in sorted(assets_by_name):
        if name.endswith(".metadata.json") and name[:-len(".metadata.json")] + ".nstudio" not in assets_by_name:
            report_problem(tag, f"{name} has no matching .nstudio file")

    entries = []
    for bundle_name in bundle_names:
        metadata_name = bundle_name[:-len(".nstudio")] + ".metadata.json"
        metadata_asset = assets_by_name.get(metadata_name)
        if metadata_asset is None:
            report_problem(tag, f"{bundle_name} has no {metadata_name} next to it")
            continue
        try:
            with open_url(metadata_asset["browser_download_url"]) as response:
                manifest = json.loads(response.read().decode("utf-8-sig"))
        except Exception as error:
            report_problem(tag, f"could not read {metadata_name}: {error}")
            continue

        problem = manifest_problem(manifest, tag_match["id"], tag_match["version"], bundle_name)
        if problem:
            report_problem(tag, problem)
            continue

        bundle_asset = assets_by_name[bundle_name]
        try:
            sha256 = asset_sha256(bundle_asset)
        except Exception as error:
            report_problem(tag, f"could not hash {bundle_name}: {error}")
            continue

        entries.append({
            "manifest": manifest,
            "url": bundle_asset["browser_download_url"],
            "size": bundle_asset["size"],
            "sha256": sha256,
            "notes": (release.get("body") or "").strip(),
            "publishedAt": release.get("published_at") or "",
        })
    return entries


def version_key(version):
    return tuple(int(part) for part in version.split("."))


def build_catalog(releases):
    packs = []
    seen = set()
    for release in releases:
        if release.get("draft") or release.get("prerelease"):
            continue
        for entry in catalog_entries(release):
            manifest = entry["manifest"]
            key = (manifest["id"], manifest["version"], manifest["os"], manifest["arch"], manifest["variant"])
            if key in seen:
                report_problem(release.get("tag_name"), f"{'-'.join(key)} is published more than once")
                continue
            seen.add(key)
            packs.append(entry)

    packs.sort(key=lambda entry: (
        entry["manifest"]["id"],
        tuple(-part for part in version_key(entry["manifest"]["version"])),
        entry["manifest"]["variant"],
        entry["manifest"]["os"],
        entry["manifest"]["arch"],
    ))
    return {"schemaVersion": 1, "packs": packs}


def main():
    parser = argparse.ArgumentParser()
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--repository", help="owner/name of the releases repository")
    source.add_argument("--releases-file", help="read the releases list from a JSON file instead of GitHub")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()

    try:
        if args.releases_file:
            releases = json.loads(Path(args.releases_file).read_text(encoding="utf-8"))
        else:
            releases = list_releases(args.repository)
    except Exception as error:
        print(f"could not list releases: {error}", file=sys.stderr)
        sys.exit(2)

    catalog = build_catalog(releases)
    Path(args.output).write_text(json.dumps(catalog, indent=2, ensure_ascii=False) + "\n", encoding="utf-8", newline="\n")
    print(f"wrote {args.output}: {len(catalog['packs'])} pack(s) from {len(releases)} release(s)")

    if problems:
        print(f"{len(problems)} problem(s); the affected bundles were left out", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
