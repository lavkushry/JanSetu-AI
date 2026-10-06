# Vision smoke fixture

`vision-people.png` is OpenCV 4.12.0's unchanged `samples/data/basketball1.png`, used only for actual inference tests. [Pinned source](https://github.com/opencv/opencv/blob/cbee6841638edb6fbc8110df7cd52bb8e3d66211/samples/data/basketball1.png). SHA-256 `ba06f6701f7260998b430c39b6557f775497e6ce7b1a74f0b7ea6af371bf54a6`.

The upstream license is reproduced in [OpenCV-Apache-2.0.txt](OpenCV-Apache-2.0.txt). No modifications or resident data are included. This sample proves inference runs, not civic-hazard accuracy. See [the recognition guide](../../docs/PRIVATE_IMAGE_RECOGNITION.md).

## Recorded road-frame fixture

`road-frame.webm` is a two-second, 640×360, silent synthetic VP9 clip created for JanSetu's browser frame-selection checks. It contains colored rectangles only, with no external images or resident data. SHA-256 `295d847c489442bf3d0697b30ff59d3d14a112d157b0959d01dd9b166b0e6321`. This is a capture/upload fixture, not a pothole evaluation sample.

To recreate using FFmpeg with libvpx-vp9:

```bash
ffmpeg -hide_banner -loglevel error -f lavfi -i 'color=c=0x3c5963:s=640x360:r=5:d=2' -vf "drawbox=x=180:y=210:w=110:h=55:color=0x172027:t=fill,drawbox=x=314:y=10:w=12:h=340:color=0xededdd:t=fill" -c:v libvpx-vp9 -an -y tests/fixtures/road-frame.webm
```

See the [private road reporting guide](../../docs/POTHOLE_REPORTING.md) for capture, review and source boundaries.
