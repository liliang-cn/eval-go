#!/usr/bin/env python3
"""Score an `evalgo bench` report out of 100 using per-task weights.

The 30-task set stopped discriminating: both frameworks were at ceiling in seven
of eight categories, so a run said little beyond "both work". hard-15 replaces
count with difficulty, and weights the tasks that actually separate agents
(restraint, error recovery, fabrication) above the ones most already pass.

Partial credit matters as much as the weights. A binary pass/fail gives each
task two states; the judge's continuous 0..1 gives eleven, which is where the
resolution comes from when there are only fifteen of them.

    ./score.py results/report.json [tasks/hard-15.json]
"""
import json
import sys
from collections import defaultdict


def main() -> None:
    report_path = sys.argv[1] if len(sys.argv) > 1 else "results/report.json"
    tasks_path = sys.argv[2] if len(sys.argv) > 2 else "tasks/hard-15.json"

    tasks = json.load(open(tasks_path))
    weight = {t["name"]: int(t["meta"]["weight"]) for t in tasks}
    kind = {t["name"]: t["meta"]["type"] for t in tasks}
    total_weight = sum(weight.values())

    report = json.load(open(report_path))

    print(f"=== {report_path}  (满分 {total_weight}) ===\n")
    board = []

    for target in report["targets"]:
        by_sample = {s["sample"]: s for s in target["report"]["samples"]}
        score = 0.0
        errors = 0
        per_kind = defaultdict(lambda: [0.0, 0])
        rows = []

        for name, w in weight.items():
            sr = by_sample.get(name)
            if sr is None:
                rows.append((name, w, 0.0, "missing"))
                continue
            res = next((r for r in sr["results"] if r["metric"] == "task_completion"), None)
            if res is None:
                rows.append((name, w, 0.0, "unscored"))
                continue
            # A judge that fell over scores zero and would read as a bad agent.
            # Count those separately rather than letting them into the total.
            if res.get("error"):
                errors += 1
                rows.append((name, w, 0.0, "JUDGE ERROR"))
                continue
            got = res.get("score", 0.0) * w
            score += got
            per_kind[kind[name]][0] += got
            per_kind[kind[name]][1] += w
            rows.append((name, w, got, ""))

        board.append((target["name"], score, errors))
        print(f"--- {target['name']}: {score:.1f}/{total_weight} ---")
        for name, w, got, note in sorted(rows, key=lambda r: r[2] / r[1] if r[1] else 0):
            bar = "#" * int(round(10 * (got / w))) if w else ""
            print(f"  {got:4.1f}/{w:<2}  {name:<26} {bar}{'  ' + note if note else ''}")
        print("  按类型:")
        for k, (g, w) in sorted(per_kind.items(), key=lambda kv: kv[1][0] / kv[1][1]):
            print(f"    {g:4.1f}/{w:<3} {k}")
        if errors:
            print(f"  !! {errors} 道题的裁判报错，未计入总分 —— 分数不可信")
        print()

    if len(board) > 1:
        print("=== 排名 ===")
        for name, score, errors in sorted(board, key=lambda b: -b[1]):
            flag = f"  (裁判报错 {errors} 题)" if errors else ""
            print(f"  {score:5.1f}/{total_weight}  {name}{flag}")


if __name__ == "__main__":
    main()
