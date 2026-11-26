#!/usr/bin/env python3
"""Fold changelog.d fragments into CHANGELOG.md and print the new version.

Usage: changelog.py [repo-root]   |   changelog.py --selftest
"""
import datetime
import pathlib
import re
import sys
import tempfile

ORDER = ["Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"]
URL = "https://github.com/GuitarWag/gcp-local"


def fold(root: pathlib.Path, today: str) -> str:
    frags = sorted(p for p in (root / "changelog.d").glob("*.md") if p.name != "README.md")
    if not frags:
        raise SystemExit("no changelog fragments")

    sections = {s: [] for s in ORDER}
    bump = "patch"
    for p in frags:
        head, _, body = p.read_text().partition("\n")
        parts = head.split()
        if len(parts) != 2 or parts[0] not in ORDER or parts[1] not in ("minor", "patch"):
            raise SystemExit(f"{p}: first line must be '<Section> <minor|patch>', got {head!r}")
        sections[parts[0]].append(body.strip())
        if parts[1] == "minor":
            bump = "minor"

    path = root / "CHANGELOG.md"
    log = path.read_text()
    m = re.search(r"^## \[(\d+)\.(\d+)\.(\d+)\]", log, re.M)
    major, minor, patch = map(int, m.groups())
    old = f"{major}.{minor}.{patch}"
    ver = f"{major}.{minor + 1}.0" if bump == "minor" else f"{major}.{minor}.{patch + 1}"

    block = [f"## [{ver}] - {today}"]
    for s in ORDER:
        if sections[s]:
            block += ["", f"### {s}", "", "\n\n".join(sections[s])]
    if "## [Unreleased]\n" not in log:
        raise SystemExit("CHANGELOG.md has no '## [Unreleased]' heading")
    log = log.replace("## [Unreleased]\n", "## [Unreleased]\n\n" + "\n".join(block) + "\n", 1)
    log, n = re.subn(
        rf"^\[Unreleased\]: {re.escape(URL)}/compare/v{re.escape(old)}\.\.\.HEAD$",
        f"[Unreleased]: {URL}/compare/v{ver}...HEAD\n[{ver}]: {URL}/releases/tag/v{ver}",
        log,
        flags=re.M,
    )
    if n != 1:
        raise SystemExit(f"CHANGELOG.md: no [Unreleased] footnote for v{old}")

    path.write_text(log)
    for p in frags:
        p.unlink()
    return ver


def selftest() -> None:
    with tempfile.TemporaryDirectory() as d:
        root = pathlib.Path(d)
        (root / "changelog.d").mkdir()
        (root / "changelog.d" / "README.md").write_text("keep\n")
        (root / "changelog.d" / "1-a.md").write_text("Fixed patch\n- **A.** Closes #1.\n")
        (root / "changelog.d" / "2-b.md").write_text("Added minor\n- **B.** Closes #2.\n")
        (root / "CHANGELOG.md").write_text(
            "# Changelog\n\n## [Unreleased]\n\n## [0.12.0] - 2026-11-26\n\n- old\n\n"
            f"[Unreleased]: {URL}/compare/v0.12.0...HEAD\n[0.12.0]: {URL}/releases/tag/v0.12.0\n"
        )
        assert fold(root, "2026-11-27") == "0.13.0"
        out = (root / "CHANGELOG.md").read_text()
        assert out.index("## [Unreleased]") < out.index("## [0.13.0] - 2026-11-27") < out.index("## [0.12.0]")
        assert out.index("### Added") < out.index("### Fixed")
        assert f"[Unreleased]: {URL}/compare/v0.13.0...HEAD\n[0.13.0]: {URL}/releases/tag/v0.13.0\n[0.12.0]" in out
        assert [p.name for p in (root / "changelog.d").iterdir()] == ["README.md"]
        (root / "changelog.d" / "3-c.md").write_text("Fixed patch\n- **C.**\n")
        assert fold(root, "2026-11-28") == "0.13.1"
    print("selftest ok")


if __name__ == "__main__":
    if sys.argv[1:] == ["--selftest"]:
        selftest()
    else:
        print(fold(pathlib.Path(sys.argv[1] if len(sys.argv) > 1 else "."),
                   datetime.datetime.now(datetime.timezone.utc).date().isoformat()))
