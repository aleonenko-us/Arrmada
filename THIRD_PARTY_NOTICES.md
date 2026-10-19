# Third-party notices

Arrmada is MIT-licensed. This file lists third-party code whose licence asks for a notice
beyond the ordinary attribution in `go.mod` and `web/package.json`.

## Audio tags (`internal/audiotag`) — not in the shipped binary yet

`internal/audiotag` reads and writes music tags. **Nothing in Arrmada imports it yet**, so
the Go toolchain doesn't link it, or the libraries below, into the `arrmada` binary or the
Docker image: what ships stays MIT/Apache-licensed. That changes when the music import
starts tagging files (the music rebuild's Phase 2); this file then ships with the image,
and the entries below apply to it.

By the owner's decision of 2026-10-19 the package writes FLAC, MP3 and MP4/M4A with
permissively licensed libraries, and uses TagLib only for Ogg Vorbis and Opus, which none
of them covers.

### go-taglib — GNU Lesser General Public License v2.1

- Module: `go.senan.xyz/taglib` (v0.15.0), source: https://github.com/sentriz/go-taglib
- It embeds TagLib 2 (https://taglib.org, LGPL-2.1 / MPL-1.1, source:
  https://github.com/taglib/taglib) compiled to WebAssembly, as the file `taglib.wasm`.
- Licence text: https://www.gnu.org/licenses/old-licenses/lgpl-2.1.html

**Replacing the LGPL part.** The TagLib code runs from that one WebAssembly file, which
can be swapped for a build of your own without changing Arrmada: build Arrmada with

```
go build -ldflags="-X 'go.senan.xyz/taglib.binaryPath=/path/to/taglib.wasm'" ./cmd/arrmada
```

and go-taglib loads the file at that path instead of its embedded copy. go-taglib's
repository has the build for `taglib.wasm` (CMakeLists.txt, taglib.cpp). Arrmada's own
source is public, so the whole program can also be rebuilt against a modified go-taglib
with a `replace` line in `go.mod`.

### wazero — Apache License 2.0

- Module: `github.com/tetratelabs/wazero`, source: https://github.com/tetratelabs/wazero
- The WebAssembly runtime go-taglib runs TagLib in.
- Licence text: https://www.apache.org/licenses/LICENSE-2.0

### The permissive tag libraries

- `github.com/go-flac/go-flac` and `github.com/go-flac/flacvorbis` — Apache License 2.0
- `github.com/bogem/id3v2/v2` — MIT
- `github.com/abema/go-mp4` — MIT
