#!/usr/bin/env python3
"""Run Claude Code on open ike issues (> 1602), one fresh session per issue.

With issue numbers as arguments (`issue_runner.py 1633 1634`) only those issues
run, in the given order; they must be open, the MIN_ISSUE bound does not apply.
Without arguments: fetches all open issues each round, picks the oldest with number > 1602,
hands number/title/body/comments to a headless Claude session (model/effort
from the issue's `model:*` / `effort:*` labels, default fable/medium), and
repeats until no matching issue is left. Progress goes to stdout
and issue_runner.log.

Epic issues (label `epic`) are spec containers: scan mode skips them; naming
one explicitly runs its open sub-issues (from the `- [ ] #N` task list) in
list order instead.
"""

import json
import logging
import re
import subprocess
import sys
import time
from pathlib import Path

REPO = "TrueDaerk/ike"
MIN_ISSUE = 1602  # exclusive lower bound
WORKDIR = Path(__file__).resolve().parent
LOG_FILE = WORKDIR / "issue_runner.log"

log = logging.getLogger("issue_runner")


def setup_logging() -> None:
    log.setLevel(logging.DEBUG)
    fmt = logging.Formatter("%(asctime)s %(levelname)-7s %(message)s")
    fh = logging.FileHandler(LOG_FILE, encoding="utf-8")
    fh.setFormatter(fmt)
    fh.setLevel(logging.DEBUG)
    sh = logging.StreamHandler(sys.stdout)
    sh.setFormatter(fmt)
    sh.setLevel(logging.INFO)
    log.addHandler(fh)
    log.addHandler(sh)


def run(cmd: list[str], **kwargs) -> subprocess.CompletedProcess:
    log.debug("exec: %s", " ".join(cmd))
    return subprocess.run(cmd, capture_output=True, text=True, **kwargs)


def open_issue_labels() -> dict[int, set[str]]:
    """Open issue numbers mapped to their label names."""
    proc = run([
        "gh", "issue", "list", "--repo", REPO, "--state", "open",
        "--limit", "500", "--json", "number,labels",
    ])
    if proc.returncode != 0:
        log.error("gh issue list failed: %s", proc.stderr.strip())
        sys.exit(1)
    return {
        i["number"]: {l.get("name", "") for l in i.get("labels") or []}
        for i in json.loads(proc.stdout)
    }


def open_issues() -> set[int]:
    return set(open_issue_labels())


def next_issue() -> int | None:
    """Lowest open non-epic issue number > MIN_ISSUE, or None.

    Epic issues are spec containers worked through their sub-issues; handing
    one to a Claude session would treat the spec as a work item and trip the
    still-open loop guard afterwards.
    """
    labels = open_issue_labels()
    numbers = [n for n, ls in labels.items() if n > MIN_ISSUE and "epic" not in ls]
    skipped = [n for n, ls in labels.items() if n > MIN_ISSUE and "epic" in ls]
    for n in sorted(skipped):
        log.debug("skipping epic issue #%d in scan mode", n)
    return min(numbers) if numbers else None


def is_epic(issue: dict) -> bool:
    return any((l.get("name") == "epic") for l in issue.get("labels") or [])


def epic_subissues(issue: dict) -> list[int]:
    """Sub-issue numbers from the epic's `- [ ] #N` task list, in list order.

    Ticked boxes (`- [x] #N`) are included too — openness is checked against
    the live issue state, not the checkbox, since task-list ticks can lag.
    """
    return [
        int(m.group(1))
        for m in re.finditer(r"^\s*-\s*\[[ xX]\]\s*#(\d+)",
                             issue.get("body") or "", re.MULTILINE)
    ]


def issue_details(number: int) -> dict:
    proc = run([
        "gh", "issue", "view", str(number), "--repo", REPO,
        "--json", "number,title,body,comments,labels",
    ])
    if proc.returncode != 0:
        log.error("gh issue view %d failed: %s", number, proc.stderr.strip())
        sys.exit(1)
    return json.loads(proc.stdout)


def model_and_effort(issue: dict) -> tuple[str, str]:
    """Model/effort from `model:*` / `effort:*` labels; default fable/medium."""
    model, effort = "fable", "medium"
    for label in issue.get("labels") or []:
        name = label.get("name", "")
        if name.startswith("model:"):
            model = name.removeprefix("model:")
        elif name.startswith("effort:"):
            effort = name.removeprefix("effort:")
    return model, effort


def build_prompt(issue: dict) -> str:
    parts = [
        f"Work on GitHub issue #{issue['number']} of {REPO}. "
        "All issue information is included below — do not fetch the issue yourself.",
        "",
        f"# Issue #{issue['number']}: {issue['title']}",
        "",
        issue.get("body") or "(no body)",
    ]
    comments = issue.get("comments") or []
    if comments:
        parts += ["", "# Comments"]
        for c in comments:
            author = (c.get("author") or {}).get("login", "unknown")
            parts += ["", f"## {author} ({c.get('createdAt', '')})", c.get("body", "")]
    parts += [
        "",
        "# Instructions",
        "Follow the repository change workflow (CLAUDE.md / wiki/process/change-workflow.md):",
        "work on a branch `issue/<number>-<slug>` off an up-to-date main, implement the issue "
        "with tests, update the wiki where behavior changes, run docgen if commands/keybinds "
        "changed, bump the patch version in its own chore(version) commit, open a PR with "
        f"`Closes #{issue['number']}`, merge it, delete the branch, and return to main.",
    ]
    return "\n".join(parts)


def describe_tool_use(name: str, tool_input: dict) -> str | None:
    """One-line summary of an important tool call, or None to skip it.

    Only state-changing / long-running calls are reported: shell commands,
    file writes, subagents. Reads, searches and plain text are noise here.
    """
    if name == "Bash":
        detail = tool_input.get("description") or tool_input.get("command", "")
    elif name in ("Edit", "Write", "NotebookEdit"):
        detail = tool_input.get("file_path", "")
    elif name in ("Agent", "Task"):
        detail = tool_input.get("description", "")
    elif name == "Skill":
        detail = tool_input.get("skill", "")
    else:
        return None
    detail = " ".join(detail.split())
    if len(detail) > 160:
        detail = detail[:157] + "..."
    return f"{name}: {detail}" if detail else name


def stream_event(event: dict) -> None:
    """Log a single stream-json event from the Claude session."""
    etype = event.get("type")
    if etype == "assistant":
        for block in (event.get("message") or {}).get("content") or []:
            if block.get("type") != "tool_use":
                continue
            summary = describe_tool_use(block.get("name", "?"),
                                        block.get("input") or {})
            if summary:
                log.info("  %s", summary)
    elif etype == "result":
        log.info("  result: %s (turns=%s, cost=$%.2f)",
                 event.get("subtype", "?"), event.get("num_turns", "?"),
                 event.get("total_cost_usd") or 0.0)


def run_claude(prompt: str, model: str, effort: str) -> int:
    proc = subprocess.Popen(
        [
            "claude", "-p", prompt,
            "--model", model,
            "--effort", effort,
            "--dangerously-skip-permissions",
            "--output-format", "stream-json",
            "--verbose",
        ],
        cwd=WORKDIR, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
    )
    assert proc.stdout is not None
    for line in proc.stdout:
        line = line.strip()
        if not line:
            continue
        try:
            stream_event(json.loads(line))
        except json.JSONDecodeError:
            log.debug("claude non-json line: %s", line)
    stderr = proc.stderr.read() if proc.stderr else ""
    if stderr.strip():
        log.debug("claude stderr:\n%s", stderr.strip())
    return proc.wait()


def work_issue(number: int) -> None:
    """Run one Claude session on the issue; exit on failure or if it stays open.

    An epic issue is expanded instead: its open sub-issues run one by one in
    task-list order, the epic itself is never handed to a session.
    """
    issue = issue_details(number)
    if is_epic(issue):
        work_epic(issue)
        return
    model, effort = model_and_effort(issue)
    log.info("starting issue #%d (%s/%s): %s", number, model, effort, issue["title"])
    start = time.monotonic()
    rc = run_claude(build_prompt(issue), model, effort)
    minutes = (time.monotonic() - start) / 60
    log.info("claude exited rc=%d after %.1f min on issue #%d", rc, minutes, number)

    if rc != 0:
        log.error("claude failed on issue #%d — stopping", number)
        sys.exit(1)
    if number in open_issues():
        log.error("issue #%d still open after run — stopping to avoid a loop", number)
        sys.exit(1)


def work_epic(issue: dict) -> None:
    """Run an epic's open sub-issues in task-list order."""
    number = issue["number"]
    subs = epic_subissues(issue)
    if not subs:
        log.error("epic #%d has no task-list sub-issues — nothing to run", number)
        sys.exit(1)
    still_open = open_issues()
    todo = [n for n in subs if n in still_open]
    log.info("epic #%d: %d sub-issue(s), %d open: %s", number, len(subs),
             len(todo), ", ".join(f"#{n}" for n in todo) or "-")
    for sub in todo:
        work_issue(sub)
    if number in open_issues():
        log.info("epic #%d: all sub-issues done but the epic is still open — "
                 "close it (and its milestone) manually", number)


def parse_args(argv: list[str]) -> list[int]:
    """Issue numbers from argv (e.g. `issue_runner.py 1633 1634`); empty = scan mode."""
    try:
        return [int(a.lstrip("#")) for a in argv]
    except ValueError:
        print(f"usage: {sys.argv[0]} [issue-number ...]", file=sys.stderr)
        sys.exit(2)


def main() -> None:
    selected = parse_args(sys.argv[1:])
    setup_logging()

    if selected:
        log.info("issue runner started (repo=%s, selected: %s)",
                 REPO, ", ".join(f"#{n}" for n in selected))
        still_open = open_issues()
        closed = [n for n in selected if n not in still_open]
        if closed:
            log.error("not open: %s — aborting", ", ".join(f"#{n}" for n in closed))
            sys.exit(1)
        for number in selected:
            work_issue(number)
        log.info("done after %d issue(s)", len(selected))
        return

    log.info("issue runner started (repo=%s, issues > %d)", REPO, MIN_ISSUE)
    done = 0
    while True:
        number = next_issue()
        if number is None:
            log.info("no open issues > %d left — done after %d issue(s)", MIN_ISSUE, done)
            return
        work_issue(number)
        done += 1


if __name__ == "__main__":
    main()
