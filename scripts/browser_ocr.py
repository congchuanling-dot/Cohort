#!/usr/bin/env python3
"""Run RapidOCR on one image and emit a single JSON result to stdout."""

import argparse
import json
import re
import sys
from pathlib import Path


def error(code, message, hint):
    print(
        json.dumps(
            {
                "status": "error",
                "code": code,
                "message": message,
                "hint": hint,
            },
            ensure_ascii=False,
        )
    )


def parse_args():
    parser = argparse.ArgumentParser(description="Cohert browser OCR helper")
    parser.add_argument("--image", required=True, help="Path to an image file")
    parser.add_argument(
        "--min-confidence",
        type=float,
        default=0.5,
        help="Discard OCR lines below this confidence threshold",
    )
    parser.add_argument(
        "--enhance",
        action="store_true",
        help="Apply contrast and scale preprocessing; disabled by default",
    )
    return parser.parse_args()


def strip_cjk_spaces(text):
    return re.sub(r"(?<=[\u4e00-\u9fff])\s+(?=[\u4e00-\u9fff])", "", text)


def preprocess(image):
    from PIL import ImageEnhance

    image = ImageEnhance.Contrast(image).enhance(3.0)
    return image.resize((image.width * 3, image.height * 3))


def normalize_bbox(points, scale):
    xs = [float(point[0]) / scale for point in points]
    ys = [float(point[1]) / scale for point in points]
    return [
        int(round(min(xs))),
        int(round(min(ys))),
        int(round(max(xs))),
        int(round(max(ys))),
    ]


def main():
    args = parse_args()
    if args.min_confidence < 0 or args.min_confidence > 1:
        error(
            "browser_ocr_bad_min_confidence",
            "min-confidence must be between 0 and 1",
            "请传入 0 到 1 之间的 min_confidence，默认值为 0.5。",
        )
        return 0

    image_path = Path(args.image)
    if not image_path.is_file():
        error(
            "browser_ocr_image_not_found",
            f"OCR image does not exist: {image_path}",
            "请提供 workspace 内存在的图片路径，或不传 image_path 让工具自动截图。",
        )
        return 0

    try:
        import numpy as np
        from PIL import Image
        from rapidocr_onnxruntime import RapidOCR
    except ModuleNotFoundError as exc:
        error(
            "browser_ocr_dependency_missing",
            f"Python OCR dependency is missing: {exc.name}",
            "请手动安装依赖：python3 -m pip install rapidocr-onnxruntime pillow numpy。",
        )
        return 0

    try:
        with Image.open(image_path) as source:
            source = source.convert("RGB")
            width, height = source.size
            scale = 3 if args.enhance else 1
            image = preprocess(source) if args.enhance else source
            result, _ = RapidOCR()(np.array(image))
    except Exception as exc:  # OCR engines expose implementation-specific errors.
        error(
            "browser_ocr_image_unreadable",
            f"Unable to OCR image: {exc}",
            "请确认图片格式可读且没有损坏；必要时重新调用 browser_screenshot。",
        )
        return 0

    lines = []
    for item in result or []:
        if len(item) < 3:
            continue
        bbox, text, confidence = item[0], str(item[1]).strip(), float(item[2])
        if not text:
            continue
        if confidence < args.min_confidence:
            continue
        normalized = normalize_bbox(bbox, scale)
        lines.append(
            {
                "index": len(lines) + 1,
                "text": strip_cjk_spaces(text),
                "confidence": confidence,
                "bbox": normalized,
                "center": {
                    "x": (normalized[0] + normalized[2]) // 2,
                    "y": (normalized[1] + normalized[3]) // 2,
                },
            }
        )

    print(
        json.dumps(
            {
                "status": "success",
                "width": width,
                "height": height,
                "text": "\n".join(line["text"] for line in lines),
                "lines": lines,
            },
            ensure_ascii=False,
        )
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
