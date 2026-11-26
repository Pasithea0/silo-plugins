# Pasithea Silo Plugins

Plugins for [Silo](https://siloserver.org), the self-hosted media server.

---

### Add this catalog

In Silo, open **Admin → Plugins → Repositories** and add a repository with any name:

```
https://raw.githubusercontent.com/Pasithea0/silo-plugins/main/manifest.json
```

Then install a plugin **from the catalog** rather than by uploading a file: only
a catalog install is linked to the repository, and only a linked install can
update itself.

---

### Plugins

| Plugin | Description |
| --- | --- |
| [Tiny Subtitles](https://github.com/Pasithea0/silo-plugin-tiny-subtitles) | Generates subtitles on the server that hosts Silo: a local speech recognizer, then word-accurate timings. |
| [Link Previews](https://github.com/Pasithea0/silo-plugin-link-previews) | Rich link previews for items shared to Discord, Signal, Slack and friends. |

---

### Updates

Silo's **Check Plugin Updates** task fetches every enabled repository once a day
and applies or records available updates per installation. Each installation has
an update policy — `auto`, `notify`, `off` or `manual` — changeable on the
plugin's detail page.

---

### How this repository works

`manifest.json` is the catalog the host reads. It is generated, not written by hand:

- [`plugins.json`](plugins.json) lists which plugin repositories belong here.
- [`scripts/build_catalog.py`](scripts/build_catalog.py) reads each plugin's latest
  GitHub release, takes the manifest at that tag, and records the binary and its
  checksum for every platform the release ships.
- [`tools/verify`](tools/verify) validates the result with the same SDK rules the
  host applies when it reads a repository, and checks that every binary downloads
  and reports the ID and version the catalog claims.

CI rebuilds the catalog hourly and on dispatch, and commits only when something
changed.

MIT
