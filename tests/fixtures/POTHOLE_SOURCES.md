# Public road-photo smoke fixtures

These are licensed test images, never resident uploads. Visual labels in
[pothole-smoke.json](pothole-smoke.json) were written during implementation review.
They are not independently adjudicated human ground truth. They predate the model;
overlap with its web-sourced training images is unknown.

| Local file | Original source / author | Declaration | Changes |
| --- | --- | --- | --- |
| pothole-positive.png | [Pothole on local Road in County Monaghan](https://commons.wikimedia.org/wiki/File:Pothole_on_local_Road_in_County_Monaghan.jpg), Computerfan0 | [CC0](https://creativecommons.org/publicdomain/zero/1.0/) | Decoded, resized proportionally to 583×1200, PNG, metadata removed |
| pothole-big.png | [Pothole Big](https://commons.wikimedia.org/wiki/File:Pothole_Big.jpg), Uncl3dad | Uploader public-domain dedication on source page | Decoded to 400×300 PNG, metadata removed |
| pothole-villeray.png | [Pothole in Villeray, Montréal](https://commons.wikimedia.org/wiki/File:Pothole_in_Villeray,_Montr%C3%A9al.jpg), Miguel Tremblay | Uploader public-domain dedication on source page | Decoded, resized proportionally to 1200×900, PNG, metadata removed |
| road-texture.png | [Asphalt road texture](https://commons.wikimedia.org/wiki/File:Asphalt_road_texture.jpg), Kurt Kaiser | [CC0](https://creativecommons.org/publicdomain/zero/1.0/) | Decoded, resized proportionally to 1200×900, PNG, metadata removed |
| road-plain.png | [Asphalt road at Shiashie](https://commons.wikimedia.org/wiki/File:Asphalt_road_at_Shiashie.jpg), Masssly | [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/) | Decoded, resized proportionally to 675×1200, PNG, metadata removed; derivative under same license |
| road-sunset.png | [Asphalt road and sunset](https://commons.wikimedia.org/wiki/File:Asphalt_road_and_sunset.jpg), Diellza Buzhala | [CC BY-SA 4.0](https://creativecommons.org/licenses/by-sa/4.0/) | Decoded, resized proportionally to 673×1200, PNG, metadata removed; derivative under same license |

Per-file SHA-256 values are recorded in the JSON manifest. Its loose boxes mark
visible cavities only; absence of boxes does not certify an intact or safe road.
The Villeray photo also contains a glove that produces a false candidate; this is
retained and reported, not removed to improve the result. These photo licenses
apply independently of JanSetu's code license. Attribution and license links must
accompany redistribution of the two ShareAlike derivatives.
