#!/usr/bin/env python3
"""Minimal eval runner for /api/chat — reads JSONL cases, posts each one,
parses the NDJSON stream, and reports per-case verdicts.

Replaces scripts/eval.sh's bash+heredoc+python chain (which had quoting
issues under Git Bash on Windows).
"""

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.request
from pathlib import Path


def post_chat(api_base: str, case_id: str, content: str, user_id: str, timeout: float = 30.0):
    url = api_base.rstrip("/") + "/api/chat"
    body = json.dumps({"userId": user_id, "content": content}).encode("utf-8")
    req = urllib.request.Request(url, data=body, method="POST", headers={"Content-Type": "application/json"})
    events: list[dict] = []
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for raw in resp:
                line = raw.decode("utf-8", errors="replace").rstrip("\n")
                if not line.strip():
                    continue
                try:
                    ev = json.loads(line)
                    ev["__latency_ms"] = int((time.time() - started) * 1000)
                    events.append(ev)
                except Exception:
                    pass
        return events, None
    except urllib.error.HTTPError as e:
        # Stream may already be partially sent; just record the error.
        return events, f"HTTP {e.code}: {e.reason}"
    except Exception as e:
        return events, f"{type(e).__name__}: {e}"


def summarise(case_id: str, expected: dict, events: list[dict], err: str | None) -> tuple[bool, str]:
    if err:
        if expected.get("expectError400"):
            return True, f"expected 400, got {err}"
        return False, err

    types = [e.get("type") for e in events]
    tool_calls = [e for e in events if e.get("type") == "tool_call"]
    tool_results = [e for e in events if e.get("type") == "tool_result"]
    final = next((e for e in events if e.get("type") == "final"), None)
    handover = next((e for e in events if e.get("type") == "handover"), None)
    pending = next((e for e in events if e.get("type") == "pending_human"), None)
    errors = [e for e in events if e.get("type") == "error"]

    notes: list[str] = []
    ok = True

    # Smoke: at least one terminal event.
    if not (final or handover or pending):
        ok = False
        notes.append("no terminal event (final|handover|pending)")

    if expected.get("expectTool"):
        names = [tc.get("tool") for tc in tool_calls]
        if expected["expectTool"] not in names:
            ok = False
            notes.append(f"expected tool {expected['expectTool']!r}, got {names}")

    if "expectPendingHuman" in expected and expected["expectPendingHuman"]:
        if not pending:
            ok = False
            notes.append("expected pending_human event, none")

    if "expectHandedOver" in expected and expected["expectHandedOver"] != bool(handover):
        ok = False
        notes.append(f"expected handedOver={expected['expectHandedOver']}, got {bool(handover)}")

    if expected.get("expectFinalContains"):
        if not final or expected["expectFinalContains"] not in (final.get("content") or ""):
            content_preview = (final.get("content") if final else "")[:80]
            ok = False
            notes.append(f"expected final to contain {expected['expectFinalContains']!r}, got {content_preview!r}")

    if expected.get("expectHandedOverReason") and (not handover or handover.get("reason") != expected["expectHandedOverReason"]):
        ok = False
        notes.append(f"expected handover reason={expected['expectHandedOverReason']!r}, got {(handover or {}).get('reason')!r}")

    if "expectStepsLeq" in expected and len([t for t in types if t == "step"]) > expected["expectStepsLeq"]:
        ok = False
        notes.append(f"too many steps ({len([t for t in types if t == 'step'])} > {expected['expectStepsLeq']})")

    if "expectToolCallsGe" in expected and len(tool_calls) < expected["expectToolCallsGe"]:
        ok = False
        notes.append(f"too few tool calls ({len(tool_calls)} < {expected['expectToolCallsGe']})")

    if errors and not expected.get("expectHandedOver"):
        # Errors are usually bad unless we expect handover (LLM-down).
        notes.append(f"stream had {len(errors)} error event(s)")
        if not handover:
            ok = False

    label = "PASS" if ok else "FAIL"
    detail = f"steps={types.count('step')} tools={[t.get('tool') for t in tool_calls]} handed={bool(handover)} pending={bool(pending)} final={(final.get('content','') if final else '')[:40]!r}"
    msg = f"  [{label}] {case_id}: {detail}"
    for n in notes:
        msg += f"\n      note: {n}"
    return ok, msg


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--api", default=os.environ.get("API_BASE", "http://localhost:8080"))
    ap.add_argument("--file", default=str(Path(__file__).parent / "eval.jsonl"))
    ap.add_argument("--user", default="eval-user")
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()

    cases: list[tuple[str, dict]] = []
    with open(args.file, encoding="utf-8") as f:
        for ln in f:
            ln = ln.strip()
            if not ln:
                continue
            try:
                cases.append((json.loads(ln).get("id", f"line-{len(cases)}"), json.loads(ln)))
            except Exception as e:
                print(f"  [SKIP] malformed line: {e}")
    if args.limit:
        cases = cases[: args.limit]

    total = len(cases)
    passed = 0
    for case_id, case in cases:
        events, err = post_chat(args.api, case_id, case.get("input", ""), args.user)
        ok, msg = summarise(case_id, case, events, err)
        print(msg)
        if ok:
            passed += 1
    print("-----")
    print(f"Eval: {passed}/{total} passed")
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())