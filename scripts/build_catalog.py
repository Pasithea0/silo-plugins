#!/usr/bin/env python3
"""Builds manifest.json, the plugin catalog Silo reads.

The catalog is a list of plugins, one entry per plugin, each pointing at the
binaries in that plugin's latest GitHub release:

    {"plugins": [{"manifest": {...}, "repo_url": ..., "binaries": {...}}]}

Silo fetches this file, matches the entry against the platform it runs on, and
downloads the binary for that platform. The binary is verified against the
checksum here, and the manifest it carries is what actually gets installed —
the manifest in this file is what the catalog lists (name, description,
version, platforms).

Everything is derived from the release, so nothing here can drift from what was
published: the manifest is read from the release's tag and every checksum comes
from the release's own checksums.txt (or from the asset itself when a release
does not ship one).
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.request

API = "https://api.github.com"
RAW = "https://raw.githubusercontent.com"
BINARY = re.compile(r"^plugin-([a-z0-9]+)-([a-z0-9]+)$")
SILO_API_VERSION = "v1"


def log(message: str) -> None:
    print(message, file=sys.stderr)


def fetch(url: str, *, binary: bool = False) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "silo-plugins-catalog"})
    token = os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN")
    if token and url.startswith(API):
        request.add_header("Authorization", f"Bearer {token}")
        request.add_header("Accept", "application/vnd.github+json")
    with urllib.request.urlopen(request, timeout=60) as response:
        return response.read()


def latest_release(repository: str) -> dict:
    return json.loads(fetch(f"{API}/repos/{repository}/releases/latest"))


def plugin_manifest(repository: str, tag: str) -> dict:
    raw = fetch(f"{RAW}/{repository}/{tag}/manifest.json")
    return json.loads(raw)


def release_checksums(assets: dict[str, dict]) -> dict[str, str]:
    """Maps asset name to sha256, from checksums.txt when the release has one."""
    if "checksums.txt" in assets:
        text = fetch(assets["checksums.txt"]["browser_download_url"]).decode("utf-8")
        checksums = {}
        for line in text.splitlines():
            parts = line.split()
            if len(parts) == 2:
                checksums[parts[1].lstrip("*")] = parts[0].lower()
        if checksums:
            return checksums
        log("  checksums.txt is empty, hashing the binaries instead")

    checksums = {}
    for name, asset in assets.items():
        if not BINARY.match(name):
            continue
        digest = hashlib.sha256(fetch(asset["browser_download_url"], binary=True)).hexdigest()
        checksums[name] = digest
    return checksums


def entry_for(repository: str) -> dict | None:
    log(f"{repository}")
    release = latest_release(repository)
    tag = release["tag_name"]
    version = tag[1:] if tag.startswith("v") else tag
    log(f"  latest release {tag}")

    manifest = plugin_manifest(repository, tag)
    if manifest.get("version") != version:
        raise SystemExit(
            f"{repository}: the tag is {tag} but its manifest.json says "
            f"{manifest.get('version')}; tag the version the manifest declares"
        )
    if manifest.get("silo_api_version") != SILO_API_VERSION:
        log(f"  skipping: silo_api_version {manifest.get('silo_api_version')!r} is not {SILO_API_VERSION}")
        return None

    # The binary stamps its own checksum when it runs, and the catalog's
    # per-platform checksums are what actually verify a download, so the
    # manifest's single checksum field is dropped here rather than lying about
    # one platform.
    manifest.pop("checksum", None)

    assets = {asset["name"]: asset for asset in release["assets"]}
    checksums = release_checksums(assets)
    platforms = {
        f"{platform['os']}/{platform['arch']}"
        for platform in manifest.get("supported_platforms", [])
    }

    binaries = {}
    for name, asset in assets.items():
        match = BINARY.match(name)
        if not match:
            continue
        key = f"{match.group(1)}/{match.group(2)}"
        if key not in platforms:
            log(f"  {name} is not in supported_platforms, left out")
            continue
        checksum = checksums.get(name)
        if not checksum:
            log(f"  {name} has no checksum, left out")
            continue
        binaries[key] = {"url": asset["browser_download_url"], "checksum": checksum}

    if not binaries:
        raise SystemExit(f"{repository}: release {tag} ships no usable plugin binary")
    missing = sorted(platforms - set(binaries))
    if missing:
        log(f"  warning: no binary for {', '.join(missing)}")

    entry = {
        "manifest": manifest,
        "repo_url": f"https://github.com/{repository}",
        "binaries": dict(sorted(binaries.items())),
        "tag": tag,
    }
    if "checksums.txt" in assets:
        entry["checksums_url"] = assets["checksums.txt"]["browser_download_url"]
    log(f"  {manifest['plugin_id']} {version}: {len(binaries)} platform(s)")
    return entry


def main() -> int:
    with open("plugins.json", encoding="utf-8") as handle:
        configured = json.load(handle)["plugins"]

    previous = {}
    if os.path.exists("manifest.json"):
        with open("manifest.json", encoding="utf-8") as handle:
            for old in json.load(handle).get("plugins", []):
                previous[old["manifest"]["plugin_id"]] = old

    entries = []
    for item in configured:
        entry = entry_for(item["repository"])
        if entry is None:
            continue
        plugin_id = entry["manifest"]["plugin_id"]
        old = previous.get(plugin_id)
        # Reuse an unchanged entry so a scheduled rebuild commits nothing.
        if old and old.get("tag") == entry["tag"] and old.get("binaries") == entry["binaries"]:
            entries.append(old)
            continue
        entries.append(entry)

    entries.sort(key=lambda entry: entry["manifest"]["plugin_id"])
    catalog = {"plugins": entries}
    rendered = json.dumps(catalog, indent=2, ensure_ascii=False) + "\n"

    old_rendered = None
    if os.path.exists("manifest.json"):
        with open("manifest.json", encoding="utf-8") as handle:
            old_rendered = handle.read()
    if rendered == old_rendered:
        log("catalog is unchanged")
        return 0

    with open("manifest.json", "w", encoding="utf-8") as handle:
        handle.write(rendered)
    log(f"wrote manifest.json with {len(entries)} plugin(s)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
