#!/usr/bin/env python3
"""Policy engine v1 for task transitions and the final ship contract."""

from __future__ import annotations

import argparse
import json
import math
import os
from pathlib import Path
import subprocess
import sys
import tempfile


def fail(code: str, message: str) -> "None":
    print(f"{code}: {message}", file=sys.stderr)
    raise SystemExit(3)


def load(path: Path, label: str) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        fail("POLICY-SCHEMA-001", f"{label} inválido: {exc}")
    if not isinstance(value, dict):
        fail("POLICY-SCHEMA-002", f"{label} debe ser un objeto JSON")
    return value


def atomic(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def state_path(task_dir: Path) -> Path:
    return task_dir / "state.json"


def lane_transitions(policy: dict, state: dict) -> dict:
    """Transiciones vigentes para el carril de la tarea.

    Un carril sin `allowed_transitions` propio hereda el grafo por defecto
    (standard y full son el pipeline completo; express salta rfc)."""
    workflow = policy.get("workflow", {})
    lane = state.get("lane", "full")
    lane_cfg = workflow.get("lanes", {}).get(lane, {})
    return lane_cfg.get("allowed_transitions") or workflow.get("allowed_transitions", {})


def cmd_init(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    path = state_path(task_dir)
    if path.exists():
        fail("POLICY-STATE-001", f"la tarea ya tiene estado: {path}")
    policy = load(Path(args.policy), "policy")
    if policy.get("schema") != 1:
        fail("POLICY-SCHEMA-003", "policy requiere schema: 1")
    if args.budget_usd is not None and (not math.isfinite(args.budget_usd) or args.budget_usd <= 0):
        fail("POLICY-BUDGET-003", "budget_usd debe ser finito y mayor que cero")
    known_lanes = policy.get("workflow", {}).get("lanes", {"full": {}})
    if args.lane not in known_lanes:
        fail("POLICY-LANE-001", f"carril desconocido: {args.lane} (permitidos: {sorted(known_lanes)})")
    state = {
        "schema": 1,
        "task_id": task_dir.name,
        "phase": policy["workflow"]["initial_phase"],
        "lane": args.lane,
        "review_rounds": 0,
        "budget_usd": args.budget_usd,
        "spent_usd": 0.0,
        "history": [],
    }
    atomic(path, state)
    print(f"✅ {task_dir.name}: phase={state['phase']} lane={args.lane}")
    return 0


def cmd_escalate(args: argparse.Namespace) -> int:
    """Sube la tarea de carril y la re-encauza por la deliberación que saltó.

    Escalar es barato a propósito: equivocarse de carril cuesta una re-entrada
    por rfc, jamás un ship sin la deliberación que tocaba."""
    task_dir = Path(args.task_dir).resolve()
    policy = load(Path(args.policy), "policy")
    path = state_path(task_dir)
    state = load(path, "estado")
    order = policy.get("workflow", {}).get("lane_escalation", ["express", "standard", "full"])
    current_lane = state.get("lane", "full")
    if args.to not in order or current_lane not in order:
        fail("POLICY-LANE-001", f"carril desconocido: {args.to}")
    if order.index(args.to) <= order.index(current_lane):
        fail("POLICY-LANE-002", f"solo se escala hacia arriba: {current_lane} → {args.to}")
    if state.get("phase") == "blocked":
        fail("POLICY-LANE-003", "tarea bloqueada: resume antes de escalar")
    previous_phase = state.get("phase")
    # La deliberación saltada se recupera: fases posteriores a rfc regresan a rfc.
    destination = "rfc" if previous_phase in ("implement", "review", "ship") else previous_phase
    state["lane"] = args.to
    state["phase"] = destination
    state.setdefault("history", []).append({
        "from": previous_phase, "to": destination, "actor": args.actor,
        "lane": f"{current_lane}→{args.to}", "reason": args.reason,
    })
    atomic(path, state)
    print(f"⤴️  {task_dir.name}: carril {current_lane} → {args.to}, fase {destination}")
    return 0


def cmd_pause(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    policy = load(Path(args.policy), "policy")
    path = state_path(task_dir)
    state = load(path, "estado")
    allowed = policy.get("workflow", {}).get("allowed_pause_reasons", [])
    if args.reason not in allowed:
        fail("POLICY-PAUSE-001", f"motivo no permitido: {args.reason}")
    if state.get("phase") == "blocked":
        fail("POLICY-PAUSE-002", "la tarea ya está bloqueada")
    previous = state.get("phase")
    state["paused_from"] = previous
    state["phase"] = "blocked"
    state.setdefault("history", []).append({
        "from": previous, "to": "blocked", "actor": args.actor,
        "reason": args.reason, "detail": args.detail,
    })
    atomic(path, state)
    print(f"⏸️  {task_dir.name}: {args.reason}")
    return 0


def cmd_resume(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    path = state_path(task_dir)
    state = load(path, "estado")
    if state.get("phase") != "blocked" or not state.get("paused_from"):
        fail("POLICY-PAUSE-003", "la tarea no tiene una pausa reanudable")
    destination = state.pop("paused_from")
    state["phase"] = destination
    state.setdefault("history", []).append({
        "from": "blocked", "to": destination, "actor": args.actor,
    })
    atomic(path, state)
    print(f"▶️  {task_dir.name}: reanuda en {destination}")
    return 0


def cmd_record_cost(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    path = state_path(task_dir)
    state = load(path, "estado")
    if not math.isfinite(args.total_usd) or args.total_usd < 0:
        fail("POLICY-BUDGET-004", "total_usd debe ser finito y no negativo")
    previous = state.get("spent_usd", 0.0)
    if args.total_usd < previous:
        fail("POLICY-BUDGET-001", f"el costo no puede retroceder: {previous} → {args.total_usd}")
    state["spent_usd"] = args.total_usd
    atomic(path, state)
    budget = state.get("budget_usd")
    if budget is not None and args.total_usd > budget:
        fail("POLICY-BUDGET-002", f"costo ${args.total_usd:.4f} excede presupuesto ${budget:.4f}")
    print(f"✅ costo registrado: ${args.total_usd:.4f}" + (f" / ${budget:.4f}" if budget is not None else ""))
    return 0


def cmd_validate_dag(args: argparse.Namespace) -> int:
    dag = load(Path(args.dag), "DAG")
    if dag.get("schema") != 1 or not isinstance(dag.get("tasks"), list) or not dag["tasks"]:
        fail("POLICY-DAG-001", "dag.json requiere schema:1 y tasks[] no vacío")
    nodes: dict[str, list[str]] = {}
    for item in dag["tasks"]:
        if not isinstance(item, dict):
            fail("POLICY-DAG-002", "cada tarea del DAG debe ser un objeto")
        task_id, repo, deps = item.get("id"), item.get("repo"), item.get("depends_on", [])
        if not isinstance(task_id, str) or not task_id or task_id in nodes:
            fail("POLICY-DAG-003", f"id vacío o duplicado: {task_id!r}")
        if not isinstance(repo, str) or not repo or "/" in repo or ".." in repo:
            fail("POLICY-DAG-004", f"repo inválido para {task_id}: {repo!r}")
        if not isinstance(deps, list) or any(not isinstance(dep, str) for dep in deps):
            fail("POLICY-DAG-005", f"depends_on inválido para {task_id}")
        nodes[task_id] = deps
    for task_id, deps in nodes.items():
        missing = [dep for dep in deps if dep not in nodes]
        if missing:
            fail("POLICY-DAG-006", f"{task_id} depende de IDs inexistentes: {missing}")
    visiting: set[str] = set()
    visited: set[str] = set()
    def visit(node: str) -> None:
        if node in visiting:
            fail("POLICY-DAG-007", f"ciclo detectado en {node}")
        if node in visited:
            return
        visiting.add(node)
        for dependency in nodes[node]:
            visit(dependency)
        visiting.remove(node)
        visited.add(node)
    for node in nodes:
        visit(node)
    print(f"✅ DAG válido: {len(nodes)} tareas, sin ciclos")
    return 0


def cmd_transition(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    policy = load(Path(args.policy), "policy")
    path = state_path(task_dir)
    state = load(path, "estado")
    current = state.get("phase")
    allowed = lane_transitions(policy, state).get(current, [])
    if args.phase not in allowed:
        lane = state.get("lane", "full")
        fail("POLICY-TRANSITION-001", f"transición no permitida ({lane}): {current} → {args.phase}")
    rounds = state.get("review_rounds", 0)
    if args.phase == "review":
        rounds += 1
        maximum = policy.get("limits", {}).get("max_review_rounds", 3)
        if rounds > maximum:
            fail("POLICY-LIMIT-001", f"review round {rounds} excede el máximo {maximum}")
    history = state.setdefault("history", [])
    history.append({"from": current, "to": args.phase, "actor": args.actor})
    state["phase"] = args.phase
    state["review_rounds"] = rounds
    atomic(path, state)
    print(f"✅ {task_dir.name}: {current} → {args.phase}")
    return 0


def cmd_validate_ship(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    policy = load(Path(args.policy), "policy")
    state = load(state_path(task_dir), "estado")
    if state.get("schema") != 1 or state.get("task_id") != task_dir.name:
        fail("POLICY-STATE-002", "state.json no corresponde a la tarea")
    if state.get("phase") != "review":
        fail("POLICY-SHIP-001", f"ship requiere phase=review; actual={state.get('phase')}")
    maximum = policy.get("limits", {}).get("max_review_rounds", 3)
    if not isinstance(state.get("review_rounds"), int) or state["review_rounds"] > maximum:
        fail("POLICY-LIMIT-001", "review_rounds inválido o excedido")
    verdict = load(Path(args.verdict), "veredicto")
    if verdict.get("schema") != 1 or verdict.get("commit") != args.commit:
        fail("POLICY-SHIP-002", "veredicto sin schema v1 o perteneciente a otro commit")
    if verdict.get("verdict") != "pass" or verdict.get("qa") != "pass":
        fail("POLICY-SHIP-003", "review y QA deben estar en pass")
    reviewer = verdict.get("reviewer")
    implementers = verdict.get("implementation_agents")
    if policy.get("ship", {}).get("require_independent_review", True):
        if not isinstance(reviewer, str) or not reviewer:
            fail("POLICY-ROLE-001", "falta reviewer")
        if not isinstance(implementers, list) or not implementers:
            fail("POLICY-ROLE-002", "falta implementation_agents[]")
        if reviewer in implementers:
            fail("POLICY-ROLE-003", "el reviewer también figura como implementador")
    print(f"✅ política de ship válida para {task_dir.name}@{args.commit[:12]}")
    return 0


def build_parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser(description="harness policy engine v1")
    root.add_argument("--policy", default="harness-policy.json")
    sub = root.add_subparsers(dest="action", required=True)
    init = sub.add_parser("init")
    init.add_argument("task_dir")
    init.add_argument("--budget-usd", type=float)
    init.add_argument("--lane", default="full")
    init.set_defaults(func=cmd_init)
    escalate = sub.add_parser("escalate")
    escalate.add_argument("task_dir")
    escalate.add_argument("--to", required=True)
    escalate.add_argument("--actor", required=True)
    escalate.add_argument("--reason", default="")
    escalate.set_defaults(func=cmd_escalate)
    transition = sub.add_parser("transition")
    transition.add_argument("task_dir")
    transition.add_argument("phase")
    transition.add_argument("--actor", required=True)
    transition.set_defaults(func=cmd_transition)
    pause = sub.add_parser("pause")
    pause.add_argument("task_dir")
    pause.add_argument("--reason", required=True)
    pause.add_argument("--detail", required=True)
    pause.add_argument("--actor", required=True)
    pause.set_defaults(func=cmd_pause)
    resume = sub.add_parser("resume")
    resume.add_argument("task_dir")
    resume.add_argument("--actor", required=True)
    resume.set_defaults(func=cmd_resume)
    cost = sub.add_parser("record-cost")
    cost.add_argument("task_dir")
    cost.add_argument("--total-usd", required=True, type=float)
    cost.set_defaults(func=cmd_record_cost)
    dag = sub.add_parser("validate-dag")
    dag.add_argument("dag")
    dag.set_defaults(func=cmd_validate_dag)
    ship = sub.add_parser("validate-ship")
    ship.add_argument("task_dir")
    ship.add_argument("--commit", required=True)
    ship.add_argument("--verdict", required=True)
    ship.set_defaults(func=cmd_validate_ship)
    return root


if __name__ == "__main__":
    parsed = build_parser().parse_args()
    raise SystemExit(parsed.func(parsed))
