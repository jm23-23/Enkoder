# Enkoder

A small desktop GUI for re-encoding videos with ffmpeg, for Linux and Windows. Pick a source file and the video, audio and subtitle tracks you want; Enkoder builds the ffmpeg command and runs the encode in the background.

- **Video:** HEVC (H.265) via NVIDIA NVENC (`hevc_nvenc`) or the CPU (`libx265`), AV1 via SVT-AV1, or plain stream copy
- **Audio, subtitles, container metadata and chapters** are copied without re-encoding
- **HDR10** (PQ) aware: colour tags and mastering-display / MaxCLL metadata are carried over
- One Go codebase with a native webview: WebKitGTK on Linux, WebView2 on Windows
- English and Polish user interface, switchable in the window

**Downloads:** prebuilt `.deb` (Debian / Ubuntu, amd64) and `enkoder.exe` (Windows, amd64) are attached to each [GitHub Release](https://github.com/jm23-23/Enkoder/releases).

## Requirements

At run time:

- **ffmpeg** and **ffprobe** in `PATH`
- on Linux also **zenity** (file dialogs) and the window libraries `libgtk-3-0` and `libwebkit2gtk-4.1-0` (or `libwebkit2gtk-4.0`)
- on Windows **WebView2** (the Edge runtime; usually already installed)

Enkoder looks for the tools in `PATH` first, then in fixed locations: `/usr/bin` and `/usr/local/bin` on Linux, `c:\ffmpeg\bin` on Windows. If one is missing, it shows a message at startup and exits.

On Debian / Ubuntu:

```bash
sudo apt install ffmpeg zenity libgtk-3-0 libwebkit2gtk-4.1-0
```

## Building

On Linux you need Go (the version from `go.mod`) and the GTK and WebKit headers:

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
make
./enkoder
```

`./build.sh` does the same. Tests: `make test`.

### .deb package (Debian / Ubuntu)

```bash
sudo apt install dpkg-dev   # once, for dpkg-shlibdeps
make deb
sudo apt install ./dist/enkoder_0.6.0_amd64.deb
```

The package installs `/usr/bin/enkoder`, the icon and an application menu entry; apt pulls in ffmpeg, zenity and the GTK/WebKit libraries (library dependencies are computed by `dpkg-shlibdeps` from the built binary). The version comes from `version.go`; override it with `VERSION=0.6.1 make deb`. Remove it with `sudo apt remove enkoder`.

If you used `make install` before, run `make uninstall` first: the entry in `~/.local` shadows the one from the package.

### Local install

`make install` installs the program into `~/.local/bin` together with the icon and a menu entry (`make uninstall` removes them). On Wayland the taskbar icon only shows up once the `.desktop` entry is installed.

### Windows

In the project directory:

```bash
go build -ldflags="-H windowsgui" -o enkoder.exe .
```

(or `build.bat`). No C compiler is needed on Windows. It also cross-compiles from Linux with `GOOS=windows CGO_ENABLED=0`.

### Icon

The source is `assets/icon.svg` (a player breaking up into macroblocks on a film-strip background); `assets/icon.png` (256×256) is embedded in the program and set as the window icon on Linux. On Windows the `.exe` and window icon come from the `rsrc_windows_*.syso` files; regenerate them with `make winres` after changing the PNG.

### Releases

Pushing a tag `v*` (for example `v0.6.0`) runs [the release workflow](.github/workflows/release.yml), which builds the `.deb` and `enkoder.exe` and attaches them to a GitHub Release. The version is taken from the tag.

## Usage

1. Run `./enkoder` (or `enkoder.exe`).
2. **Browse…** — choose a video. Enkoder reads its tracks and suggests an output name and a CQ/CRF value based on the resolution.
3. In the **Tracks** section keep what should end up in the output. The video track is selected automatically, and so is the default (or first) audio track.
4. Set the codec, container, resolution and quality options (preset, tune, bit depth, HDR, deinterlace).
5. **Save as…** — if you want a different location or name than the suggested one. The output field can also be edited by hand.
6. **Show command** — preview the ffmpeg command without starting the encode.
7. **Encode** — start. **Cancel** stops a running encode.

The **Help** button in the window repeats these rules in short.

### Language

The **PL / EN** switch in the top right changes the language of the window, the file dialogs and the error messages. The choice is saved in `settings.json` in the user config directory (`~/.config/enkoder/` on Linux, `%AppData%\enkoder\` on Windows). On first start Enkoder follows the system language: Polish for a Polish locale, English otherwise.

At least one video track must be selected. A second encode will not start until the first one finishes or is cancelled.

### Video codec

| Option | When |
| --- | --- |
| **HEVC NVENC (NVIDIA)** | Fast encoding on the GPU. Default when ffmpeg has the `hevc_nvenc` encoder. |
| **x265 / libx265 (CPU)** | Slower, usually better quality per size. Default without NVENC. |
| **AV1 / SVT-AV1 (CPU)** | About 3× faster than x265 `slow` and 15–35% smaller files at similar quality. Always 10-bit. Older TVs may not play AV1. Available when ffmpeg has `libsvtav1`. |
| **Copy video** | Remux the video into a new container without re-encoding. Not available with scaling or deinterlacing. |

### Container

- **MKV** — recommended. Handles many tracks, DTS and PGS subtitles well.
- **MP4** — more convenient on TVs and phones. Some audio and subtitle formats may not fit.

### Resolution

**Unchanged** keeps the source frame size. **Max 2160p / 1080p / 720p / 480p** only scales down (4K can go to Full HD), keeps the aspect ratio and never upscales smaller sources. Scaling needs NVENC, libx265 or SVT-AV1.

### Quality (CQ / CRF)

A lower number means a better picture and a bigger file. **0** is maximum quality (very large files); the value is kept as is. The upper limit is 51.

After a file is loaded, the suggested CRF is:

| Resolution (longer side) | CRF |
| --- | --- |
| SD (&lt; 1000) | 17 |
| HD / FHD | 18 |
| UHD (≥ 3000) | 20 |

It applies to NVENC (CQ) and x265 (CRF); with **Copy video** it is ignored.

AV1 has its own CRF scale, **0–63**. The suggestion is 3 lower than for x265 (SD 14, HD/FHD 15, UHD 17). Measured on a 1080p Blu-ray: x265 `slow` CRF 18 gives SSIM 0.9737; SVT-AV1 preset 5 gives 0.9752 at CRF 14 and 0.9712 at CRF 17 (a 35% smaller file). The default AV1 preset is **5**: preset 4 is 2.6× slower at almost the same size, preset 6 gives 12–19% larger files.

### Preset, tune, 10-bit, HDR

- **Preset** — slower means better compression at the same CQ/CRF. libx265 defaults to `slow`, NVENC to `p7`.
- **Tune** (libx265 only): `film` / `animation` / `grain`, or none.
- **Speed** (libx265 only) — analysis shortcuts added on top of the preset. Measured on a 1080p Blu-ray (i7-11800H, preset `slow`, CRF 18):

  | Profile | x265-params | Speed | Size |
  | --- | --- | --- | --- |
  | **Quality** | — | 1× | 100% |
  | **Balanced** (default) | `rect=0:rskip=2:rskip-edge-threshold=2` | ~2–2.4× | ~99% |
  | **Fast** | as above + `limit-sao=1:me=hex:subme=2:ref=3` | ~2.5–3× | ~101% |

  On top of that, automatically: `asm=avx512` on CPUs with AVX-512 (x265 does not enable it by default, ~10%), and decoding the source on an NVIDIA GPU (`-hwaccel cuda`) so the CPU only encodes.
- **Bit depth** — **Auto** picks 10-bit (`yuv420p10le`) for HDR or sources above 8-bit, otherwise 8-bit. NVENC maps 10-bit to `p010le`.
- **HDR (PQ)** — for libx265 adds `x265-params` with `hdr-opt` and BT.2020/SMPTE 2084; for NVENC and AV1 copies the colour tags. A Dolby Vision layer does not survive this pipeline.
- **HDR10 metadata** (mastering display, MaxCLL/MaxFALL) is read from the first frame of the source. ffmpeg ≥ 7.1 carries it over by itself; for x265 and AV1 Enkoder also passes it explicitly (`master-display` / `mastering-display`) so it works with older ffmpeg. When the source metadata is clearly broken (for example primaries with x = y) or the source has dynamic HDR10+, which these encoders do not carry over, Enkoder shows a warning.
- **Deinterlace (bwdif)** — checked automatically for interlaced sources.

### Output name

The suggestion is derived from the source name:

- `x264` in the name becomes `x265`; if there is no HEVC tag, `.x265` is appended
- the selected resolution replaces a tag like `4K` / `1080p` or adds one (`1080p`, `720p`, …)
- the extension follows the container (`.mkv` or `.mp4`)

The name can be changed before starting.

### Progress and log

The bar shows the **position in the video** (the same as `time=` in the ffmpeg log), not wall-clock time. `00:07:44 / 02:16:57` means 7 minutes 44 seconds of about 2 hours 17 minutes have been encoded.

In the log, `elapsed=` is the real time spent and `speed=` how many times faster than playback (for example `3.2x`).

## What ffmpeg does

A summary of what **Show command** shows:

- video: `hevc_nvenc` (preset from the UI, VBR, CQ, AQ) or `libx265` (preset, CRF, `-pix_fmt`, optionally `-x265-params`); without filters NVENC also gets `-hwaccel cuda`, and libx265 always does when CUDA works
- NVENC: if the GPU supports it (checked with a short test at startup; Pascal has no B-frames in HEVC), also `-tune hq -rc-lookahead 32 -bf 4 -b_ref_mode middle -multipass fullres`; 10-bit goes as `-profile:v main10`
- audio and subtitles: `-c:a copy` and `-c:s copy`
- metadata / chapters: `-map_metadata 0` and `-map_chapters 0` when re-encoding
- downscaling with the `scale` filter using `lanczos`; interlaced sources: `bwdif`
- NVENC with scaling or deinterlacing: when NVDEC can decode the source (checked on one frame before starting), everything stays on the GPU: `-hwaccel_output_format cuda`, `bwdif_cuda=mode=send_field`, `scale_cuda` with `lanczos`. For 4K HDR → 1080p with deinterlacing this is about 2× faster. Sources NVDEC does not support (for example H.264 4:2:2) stay on CPU filters.
- an existing output file is overwritten (`-y`)

## License

[MIT](LICENSE). Enkoder uses [webview_go](https://github.com/webview/webview_go), [go-webview2](https://github.com/jchv/go-webview2) and [zenity](https://github.com/ncruces/zenity), all MIT-licensed.
