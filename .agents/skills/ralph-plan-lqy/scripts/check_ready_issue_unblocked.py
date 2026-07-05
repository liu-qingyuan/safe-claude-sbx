#!/usr/bin/env python3
"""Read-only blocker gate for Ralph issue-backlog runs."""
from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from collections.abc import Sequence
from dataclasses import dataclass
from typing import Any

READY_LABEL = "ready-for-agent"
BLOCKER_HEADINGS = ("被阻止", "Blocked")
NO_BLOCKER_MARKERS = ("无", "none", "no blockers", "no blocker", "可以立即开始")

EXIT_READY = 0
EXIT_ERROR = 1
EXIT_NOT_READY = 2


@dataclass(frozen=True)
class IssueInfo:
    number: int
    title: str
    state: str
    labels: frozenset[str]
    body: str


@dataclass(frozen=True)
class BlockerParseResult:
    blocker_numbers: tuple[int, ...]
    section_found: bool


def extract_blocker_section(body: str) -> str | None:
    heading_pattern = "|".join(re.escape(heading) for heading in BLOCKER_HEADINGS)
    pattern = re.compile(
        rf"^##\s+(?:{heading_pattern})\s*$\n?(.*?)(?=^##\s+|\Z)",
        flags=re.IGNORECASE | re.MULTILINE | re.DOTALL,
    )
    match = pattern.search(body or "")
    return None if match is None else match.group(1).strip()


def parse_blockers(body: str) -> BlockerParseResult:
    section = extract_blocker_section(body)
    if section is None:
        return BlockerParseResult(blocker_numbers=(), section_found=False)

    normalized = section.strip().lower()
    if not normalized or any(marker in normalized for marker in NO_BLOCKER_MARKERS):
        return BlockerParseResult(blocker_numbers=(), section_found=True)

    numbers: list[int] = []
    seen: set[int] = set()
    for raw_number in re.findall(r"(?:#|/issues/)(\d+)\b", section):
        number = int(raw_number)
        if number not in seen:
            numbers.append(number)
            seen.add(number)
    return BlockerParseResult(blocker_numbers=tuple(numbers), section_found=True)


def issue_from_gh_json(payload: dict[str, Any]) -> IssueInfo:
    labels = frozenset(str(label.get("name", "")) for label in payload.get("labels", []) if label.get("name"))
    return IssueInfo(
        number=int(payload["number"]),
        title=str(payload.get("title", "")),
        state=str(payload.get("state", "")),
        labels=labels,
        body=str(payload.get("body") or ""),
    )


def gh_issue_view(issue_number: int) -> IssueInfo:
    cmd = [
        "gh",
        "issue",
        "view",
        str(issue_number),
        "--json",
        "number,title,state,labels,body",
    ]
    completed = subprocess.run(cmd, check=False, capture_output=True, text=True)
    if completed.returncode != 0:
        message = (completed.stderr or completed.stdout).strip()
        raise RuntimeError(f"`{' '.join(cmd)}` failed: {message}")
    return issue_from_gh_json(json.loads(completed.stdout))


def not_ready_reasons(target: IssueInfo, blockers: Sequence[IssueInfo]) -> list[str]:
    reasons: list[str] = []
    if target.state.upper() != "OPEN":
        reasons.append(f"target issue is {target.state}")
    if READY_LABEL not in target.labels:
        reasons.append(f"target issue is missing `{READY_LABEL}` label")
    if not parse_blockers(target.body).section_found:
        reasons.append("missing `## 被阻止` / `## Blocked` section")

    open_blockers = [issue for issue in blockers if issue.state.upper() != "CLOSED"]
    if open_blockers:
        reasons.append("blocked by open issues: " + " ".join(f"#{issue.number}" for issue in open_blockers))
    return reasons


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Check that a ready-for-agent issue has no open blockers.")
    parser.add_argument("issue_number", type=int)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        target = gh_issue_view(args.issue_number)
        blockers = [gh_issue_view(number) for number in parse_blockers(target.body).blocker_numbers]
    except (OSError, RuntimeError, json.JSONDecodeError, KeyError, ValueError) as exc:
        print(f"ERROR #{args.issue_number}: {exc}", file=sys.stderr)
        return EXIT_ERROR

    reasons = not_ready_reasons(target, blockers)
    if reasons:
        print(f"NOT READY #{target.number} {target.title}")
        for reason in reasons:
            print(f"- {reason}")
        return EXIT_NOT_READY

    print(f"READY #{target.number} {target.title}")
    return EXIT_READY


if __name__ == "__main__":
    raise SystemExit(main())
