"""Fetch the fixed upstream release; never accept a caller-supplied URL/model."""
import hashlib
from pathlib import Path
import urllib.request

SHA256 = "c789161ed43c8269fcd4e67c67eeeb4e80c622da2eb296a20bc6007bd18a0b7d"
URL = "https://github.com/Megvii-BaseDetection/YOLOX/releases/download/0.1.1rc0/yolox_nano.onnx"
target = Path(__file__).resolve().parent / "yolox_nano.onnx"
if not target.exists() or hashlib.sha256(target.read_bytes()).hexdigest() != SHA256:
    with urllib.request.urlopen(URL, timeout=60) as response:
        data = response.read(4_000_001)
    if len(data) != 3_659_407 or hashlib.sha256(data).hexdigest() != SHA256:
        raise RuntimeError("Vision model integrity check failed")
    temporary = target.with_suffix(".tmp")
    temporary.write_bytes(data)
    temporary.replace(target)
