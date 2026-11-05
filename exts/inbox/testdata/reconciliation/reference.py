"""Offline reference worker. Live workers receive CSVs and contracts, not this file."""

import csv
import hashlib
import json
import os
import sys
import time
from collections import Counter, defaultdict
from pathlib import Path


root, role, version = Path(sys.argv[1]), sys.argv[2], int(sys.argv[3])
paths = [root / "orders.csv", root / "events.csv"]
if version == 2:
    paths.append(root / "late.csv")
fingerprint = hashlib.sha256(b"\n".join(p.read_bytes() for p in paths)).hexdigest()
base = {"version": version, "fingerprint": fingerprint}


def save(name, data):
    target = root / name
    temporary = target.with_name(target.name + f".{os.getpid()}.{time.time_ns()}.tmp")
    temporary.write_text(json.dumps(data, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    temporary.replace(target)


def rows(path):
    with path.open(encoding="utf-8", newline="") as stream:
        return list(csv.DictReader(stream))


orders = {r["order_id"]: r for r in rows(paths[0])}
source = [event for path in paths[1:] for event in rows(path)]
events = {}
for event in source:
    key = event["event_id"]
    if key in events and events[key] != event:
        raise ValueError(f"conflicting event_id: {key}")
    events[key] = event

valid, orphan, mismatch, voided = [], [], [], set()
quarantined = Counter()
for key, event in events.items():
    amount = int(event["amount_minor"])
    if amount <= 0 or event["kind"] not in ("capture", "refund", "void"):
        raise ValueError(f"invalid event: {key}")
    order = orders.get(event["order_id"])
    if order is None or order["currency"] != event["currency"]:
        (orphan if order is None else mismatch).append(key)
        quarantined[event["currency"]] += amount if event["kind"] == "capture" else -amount
        continue
    if event["kind"] == "void":
        ref = events.get(event["reference"])
        if (not ref or ref["kind"] != "capture" or ref["order_id"] != event["order_id"]
                or ref["currency"] != event["currency"] or ref["amount_minor"] != event["amount_minor"]
                or ref["event_id"] in voided):
            raise ValueError(f"invalid void reference: {key}")
        voided.add(ref["event_id"])
    valid.append(event)

if role == "ledger":
    actual = Counter()
    for event in valid:
        sign = 1 if event["kind"] == "capture" else -1
        actual[event["order_id"]] += sign * int(event["amount_minor"])
    ledger, totals = [], defaultdict(Counter)
    for key, order in sorted(orders.items()):
        expected = int(order["total_minor"]) - int(order["approved_refund_minor"])
        row = {"order_id": key, "currency": order["currency"], "expected_minor": expected,
               "actual_minor": actual[key], "difference_minor": actual[key] - expected}
        ledger.append(row)
        for field in ("expected_minor", "actual_minor", "difference_minor"):
            totals[row["currency"]][field] += row[field]
    save(f"ledger-v{version}.json", {**base, "orders": ledger, "totals": dict(totals),
         "discrepancies": [r for r in ledger if r["difference_minor"]]})
    status = "ready"
elif role == "quality":
    save(f"quality-v{version}.json", {**base, "source_rows": len(source),
         "unique_events": len(events), "duplicate_rows": len(source) - len(events),
         "orphan_events": sorted(orphan), "currency_mismatches": sorted(mismatch),
         "quarantined_minor": dict(quarantined), "void_events": len(voided), "errors": []})
    status = "ready"
elif role == "reporter":
    evidence = [root / f"{name}-v{version}.json" for name in ("ledger", "quality")]
    status = "waiting"
    if all(path.exists() for path in evidence):
        ledger, quality = [json.loads(p.read_text(encoding="utf-8")) for p in evidence]
        if any(doc.get("version") != version or doc.get("fingerprint") != fingerprint
               for doc in (ledger, quality)):
            print(f"EVIDENCE role=reporter VERSION={version} status=waiting fingerprint={fingerprint}")
            sys.exit(0)
        if quality["errors"]:
            raise ValueError("quality gate failed")
        totals = defaultdict(Counter)
        for order in ledger["orders"]:
            if order["actual_minor"] - order["expected_minor"] != order["difference_minor"]:
                raise ValueError("order arithmetic does not reconcile")
            for field in ("expected_minor", "actual_minor", "difference_minor"):
                totals[order["currency"]][field] += order[field]
        if dict(totals) != ledger["totals"]:
            raise ValueError("currency totals do not reconcile")
        report = {**base, "status": "ready", "totals": ledger["totals"],
                  "discrepancies": ledger["discrepancies"], "quality": quality}
        save(f"report-v{version}.json", report)
        lines = [f"# Reconciliation v{version}", "", f"Source SHA-256: {fingerprint}", "",
                 "Amounts are integer minor units; currencies are reported separately.", ""]
        for currency, total in sorted(report["totals"].items()):
            lines.append(f"- {currency}: expected {total['expected_minor']}, actual "
                         f"{total['actual_minor']}, difference {total['difference_minor']}")
        lines.extend(["", "## Unresolved orders", ""])
        for order in report["discrepancies"]:
            lines.append(f"- {order['order_id']} ({order['currency']}): expected "
                         f"{order['expected_minor']}, actual {order['actual_minor']}, "
                         f"difference {order['difference_minor']}")
        lines.extend(["", "## Evidence quality", "",
                      f"- Source rows: {quality['source_rows']}; unique events: {quality['unique_events']}; "
                      f"duplicate rows ignored: {quality['duplicate_rows']}",
                      f"- Unknown-order events: {', '.join(quality['orphan_events'])}",
                      f"- Currency-mismatch events: {', '.join(quality['currency_mismatches'])}",
                      "- Quarantined amounts: " + json.dumps(quality["quarantined_minor"], sort_keys=True)])
        (root / f"report-v{version}.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
        status = "ready"
else:
    raise ValueError(f"unknown worker role: {role}")

print(f"EVIDENCE role={role} VERSION={version} status={status} fingerprint={fingerprint}")
