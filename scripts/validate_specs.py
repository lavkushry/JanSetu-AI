#!/usr/bin/env python3
"""Validate the documentation suite; optionally extract executable examples.

Standard library only. This validates document contracts, not application behavior.
"""

import argparse
import json
import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
SPEC = ROOT / "docs/spec"
BLOCK = re.compile(r"^```([^\n]*)\n(.*?)^```\s*$", re.M | re.S)


def slug(title):
    title = re.sub(r"[`*_]", "", title.strip()).lower()
    return re.sub(r"[^\w\- ]", "", title).replace(" ", "-")


def anchors(body):
    result, seen = set(), {}
    for title in re.findall(r"^#{1,6} (.+)$", BLOCK.sub("", body), re.M):
        base = slug(title)
        count = seen.get(base, 0)
        seen[base] = count + 1
        result.add(base if count == 0 else f"{base}-{count}")
    return result


def luminance(color):
    values = [int(color[i:i + 2], 16) / 255 for i in (1, 3, 5)]
    values = [v / 12.92 if v <= 0.04045 else ((v + 0.055) / 1.055) ** 2.4
              for v in values]
    return sum(v * w for v, w in zip(values, (0.2126, 0.7152, 0.0722)))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--extract-dir", type=Path)
    args = parser.parse_args()
    files = sorted(ROOT.rglob("*.md"))
    # Installed packages and generated reports are not repository documentation.
    excluded = {".git", "node_modules", ".venv", ".export-venv", ".next", "test-results", "playwright-report"}
    files = [p for p in files if not excluded.intersection(p.relative_to(ROOT).parts)]
    texts = {p.resolve(): p.read_text() for p in files}
    ids = {p: anchors(body) for p, body in texts.items()}
    errors, counts = [], {"links": 0, "json": 0, "mermaid": 0, "sql": 0, "go": 0}
    extracted = {"mermaid": [], "foundation": [], "geo": [], "supplement": [], "vault": [], "go": []}
    for path, body in texts.items():
        stripped = BLOCK.sub("", body)
        if len(re.findall(r"^```", body, re.M)) % 2:
            errors.append(f"{path.name}: unbalanced code fence")
        for target in re.findall(r"\[[^\]\n]+\]\(([^)\n]+)\)", stripped):
            if target.startswith("<") and target.endswith(">"):
                target = target[1:-1]
            elif re.search(r"\s", target):
                errors.append(f"{path.name}: whitespace in unescaped link {target}")
                continue
            parsed = urlsplit(target)
            if parsed.scheme or target.startswith("//"):
                continue
            resolved = (path.parent / unquote(parsed.path)).resolve() if parsed.path else path
            counts["links"] += 1
            if not resolved.exists():
                errors.append(f"{path.name}: missing link {target}")
            elif parsed.fragment and resolved in ids and unquote(parsed.fragment) not in ids[resolved]:
                errors.append(f"{path.name}: missing anchor {target}")
        if path.parent != SPEC:
            continue
        table_width = None
        for line in stripped.splitlines():
            if line.startswith("|"):
                width = len(re.split(r"(?<!\\)\|", line)) - 2
                if table_width is not None and width != table_width:
                    errors.append(f"{path.name}: table has {width} columns, expected {table_width}: {line[:90]}")
                table_width = width
            else:
                table_width = None
        for lang, code in BLOCK.findall(body):
            if lang == "json":
                counts["json"] += 1
                try:
                    json.loads(code, parse_constant=lambda value: (_ for _ in ()).throw(ValueError(value)))
                except (ValueError, json.JSONDecodeError) as exc:
                    errors.append(f"{path.name}: invalid JSON: {exc}")
            elif lang == "mermaid":
                counts["mermaid"] += 1
                extracted["mermaid"].append(code)
            elif lang == "sql" and code.lstrip().startswith(("CREATE", "ALTER")):
                counts["sql"] += 1
                if "CREATE SCHEMA vault" in code:
                    key = "vault"
                elif "CREATE TABLE geo.release" in code:
                    key = "geo"
                elif "CREATE TABLE social.profile (" in code or "CREATE TABLE ops.agency (" in code:
                    key = "foundation"
                else:
                    key = "supplement"
                extracted[key].append(code)
            elif lang == "go" and code.startswith("package "):
                counts["go"] += 1
                extracted["go"].append(code)

    trace = texts[SPEC / "TRACEABILITY_AND_DELIVERY.md"]
    prd = texts[SPEC / "PRD.md"]
    brd = texts[SPEC / "BRD.md"]
    checks = [
        ("FR definitions", set(re.findall(r"^(?:FR-|\| FR-)(\d+)(?::| \|)", prd, re.M)), 53),
        ("FR mappings", set(re.findall(r"^\| FR-(\d+) \|", trace, re.M)), 53),
        ("UX scenarios", set(re.findall(r"^\| UX-(\d+) \|", trace, re.M)), 37),
        ("AC contracts", set(re.findall(r"^\| AC-(\d+)(?::| \|)", trace, re.M)), 68),
        ("NFR definitions", set(re.findall(r"^\| NFR-(\d+) \|", prd, re.M)), 14),
        ("BR definitions", set(re.findall(r"BR-(\d+)(?::| \|)", brd)), 13),
        ("BR mappings", set(re.findall(r"^\| BR-(\d+) \|", trace, re.M)), 13),
    ]
    for name, actual, total in checks:
        expected = {f"{n:02}" for n in range(1, total + 1)}
        if actual != expected:
            errors.append(f"{name}: missing={sorted(expected-actual)}, unexpected={sorted(actual-expected)}")

    ui = texts[SPEC / "UI_IMPLEMENTATION.md"]
    tokens = dict((name, (light, dark)) for name, light, dark in re.findall(
        r"^\| `([A-Za-z]+)` \| `(#[A-Fa-f0-9]{6})` \| `(#[A-Fa-f0-9]{6})`", ui, re.M))
    required = {"canvas", "surface", "textPrimary", "textSecondary", "action", "onAction",
                "inputBoundary", "successText", "cautionText", "criticalText"}
    if not required <= tokens.keys():
        errors.append("Missing semantic color tokens")
    else:
        for mode in range(2):
            for fg in ("textPrimary", "textSecondary", "action", "successText", "cautionText", "criticalText", "inputBoundary"):
                for bg in ("canvas", "surface"):
                    a, b = sorted((luminance(tokens[fg][mode]), luminance(tokens[bg][mode])))
                    ratio = (b + .05) / (a + .05)
                    target = 3 if fg == "inputBoundary" else 4.5
                    if ratio < target:
                        errors.append(f"Theme{mode} {fg}/{bg}: {ratio:.2f} < {target}")
            a, b = sorted((luminance(tokens["onAction"][mode]), luminance(tokens["action"][mode])))
            if (b + .05) / (a + .05) < 4.5:
                errors.append(f"Theme{mode}: primary button text contrast")

    if args.extract_dir:
        args.extract_dir.mkdir(parents=True, exist_ok=True)
        schema = extracted["foundation"] + extracted["geo"] + extracted["supplement"]
        (args.extract_dir / "schema.sql").write_text("\n\n".join(schema))
        (args.extract_dir / "vault.sql").write_text("\n\n".join(extracted["vault"]))
        (args.extract_dir / "mermaid.json").write_text(json.dumps(extracted["mermaid"]))
        for i, code in enumerate(extracted["go"]):
            (args.extract_dir / f"example_{i}.go").write_text(code)
    if errors:
        print("\n".join(errors), file=sys.stderr)
        return 1
    print(f"PASS: {len(files)} Markdown files; " + ", ".join(f"{k}={v}" for k, v in counts.items()))
    print("PASS: BR01–13, FR01–53, NFR01–14, UX01–37, AC01–68; declared theme contrast pairs")
    print("Scope: documentation only; SQL/Mermaid/Go execution is a separate check.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
