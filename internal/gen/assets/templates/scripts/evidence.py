#!/usr/bin/env python3
"""Evidence v1: execute commands and bind their result to an exact Git commit.

Only this runner creates command evidence.  ``verify`` is deliberately
read-only and fail-closed so ship.sh can use it as a deterministic gate.
The implementation uses only the Python standard library.
"""

from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import uuid

SCHEMA = 1


def die(message: str, code: int = 2) -> "None":
    print(f"EVIDENCE: {message}", file=sys.stderr)
    raise SystemExit(code)


def utc_now() -> str:
    return dt.datetime.now(dt.timezone.utc).isoformat().replace("+00:00", "Z")


def git(cwd: Path, *args: str) -> str:
    result = subprocess.run(
        ["git", *args], cwd=cwd, text=True, stdout=subprocess.PIPE,
        stderr=subprocess.PIPE, check=False,
    )
    if result.returncode:
        die(f"git {' '.join(args)} falló: {result.stderr.strip()}")
    return result.stdout.strip()


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def contained(root: Path, candidate: Path) -> Path:
    root = root.resolve()
    resolved = candidate.resolve()
    try:
        resolved.relative_to(root)
    except ValueError:
        die(f"ruta fuera de la tarea: {candidate}")
    return resolved


def atomic_json(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, ensure_ascii=False, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(tmp_name, path)
    finally:
        if os.path.exists(tmp_name):
            os.unlink(tmp_name)


def command_run(args: argparse.Namespace) -> int:
    cwd = Path(args.cwd).resolve()
    task_dir = Path(args.task_dir).resolve()
    if not cwd.is_dir():
        die(f"cwd inexistente: {cwd}")
    task_dir.mkdir(parents=True, exist_ok=True)
    before = git(cwd, "rev-parse", "HEAD")
    evidence_id = f"EV-{args.kind.upper()}-{uuid.uuid4().hex[:12]}"
    evidence_dir = task_dir / "evidence"
    evidence_dir.mkdir(parents=True, exist_ok=True)
    log_path = evidence_dir / f"{evidence_id}.log"
    started = utc_now()
    command = args.command
    if command and command[0] == "--":
        command = command[1:]
    if not command:
        die("falta el comando después de --")

    with log_path.open("wb") as log:
        process = subprocess.Popen(
            command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT
        )
        assert process.stdout is not None
        for chunk in iter(lambda: process.stdout.read(8192), b""):
            log.write(chunk)
            sys.stdout.buffer.write(chunk)
            sys.stdout.buffer.flush()
        return_code = process.wait()
        log.flush()
        os.fsync(log.fileno())

    after = git(cwd, "rev-parse", "HEAD")
    manifest = {
        "schema": SCHEMA,
        "id": evidence_id,
        "task_id": task_dir.name,
        "repo": args.repo,
        "kind": args.kind,
        "runner": args.runner,
        "commit": before,
        "commit_after": after,
        "command": command,
        "cwd": str(cwd),
        "started_at": started,
        "finished_at": utc_now(),
        "exit_code": return_code,
        "output": f"evidence/{evidence_id}.log",
        "output_sha256": sha256(log_path),
    }
    atomic_json(evidence_dir / f"{evidence_id}.json", manifest)
    print(f"\nEVIDENCE_ID={evidence_id}")
    if before != after:
        print("EVIDENCE: el comando cambió HEAD; la evidencia no es publicable", file=sys.stderr)
        return 3
    return return_code


def load_json(path: Path, label: str) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        die(f"{label} inválido ({path}): {exc}", 3)
    if not isinstance(value, dict):
        die(f"{label} debe ser un objeto JSON: {path}", 3)
    return value


def verify_one(task_dir: Path, evidence_id: str, repo: str, commit: str) -> dict:
    if not evidence_id.startswith("EV-") or "/" in evidence_id or ".." in evidence_id:
        die(f"ID de evidencia inválido: {evidence_id}", 3)
    manifest_path = contained(task_dir, task_dir / "evidence" / f"{evidence_id}.json")
    data = load_json(manifest_path, "manifiesto de evidencia")
    expected = {
        "schema": SCHEMA, "id": evidence_id, "task_id": task_dir.name,
        "repo": repo, "commit": commit, "commit_after": commit, "exit_code": 0,
    }
    for field, wanted in expected.items():
        if data.get(field) != wanted:
            die(f"{evidence_id}: {field}={data.get(field)!r}; se esperaba {wanted!r}", 3)
    if not isinstance(data.get("runner"), str) or not data["runner"].strip():
        die(f"{evidence_id}: runner vacío", 3)
    output = data.get("output")
    if not isinstance(output, str) or not output:
        die(f"{evidence_id}: output inválido", 3)
    output_path = contained(task_dir, task_dir / output)
    if not output_path.is_file():
        die(f"{evidence_id}: output inexistente: {output}", 3)
    if sha256(output_path) != data.get("output_sha256"):
        die(f"{evidence_id}: SHA-256 del output no coincide", 3)
    return data


def command_verify(args: argparse.Namespace) -> int:
    task_dir = Path(args.task_dir).resolve()
    verdict = load_json(Path(args.verdict), "veredicto")
    if verdict.get("schema") != SCHEMA:
        die("el veredicto debe declarar schema: 1", 3)
    if verdict.get("task_id") != task_dir.name:
        die("task_id del veredicto no coincide", 3)
    if verdict.get("repo") != args.repo:
        die("repo del veredicto no coincide", 3)
    if verdict.get("commit") != args.commit:
        die("el veredicto pertenece a otro commit", 3)
    evidence_ids = verdict.get("evidence")
    if not isinstance(evidence_ids, list) or not evidence_ids:
        die("el veredicto no referencia evidence[]", 3)
    seen: set[str] = set()
    manifests = []
    for evidence_id in evidence_ids:
        if not isinstance(evidence_id, str) or evidence_id in seen:
            die("evidence[] contiene IDs inválidos o duplicados", 3)
        seen.add(evidence_id)
        manifests.append(verify_one(task_dir, evidence_id, args.repo, args.commit))
    required = set(args.require_kind)
    present = {item.get("kind") for item in manifests}
    missing = sorted(required - present)
    if missing:
        die(f"faltan tipos de evidencia: {', '.join(missing)}", 3)
    print(f"✅ {len(manifests)} evidencias ligadas a {args.repo}@{args.commit[:12]}")
    return 0


def parser() -> argparse.ArgumentParser:
    root = argparse.ArgumentParser(description="evidence v1")
    sub = root.add_subparsers(dest="action", required=True)
    run = sub.add_parser("run", help="ejecuta y registra un comando")
    run.add_argument("--task-dir", required=True)
    run.add_argument("--repo", required=True)
    run.add_argument("--runner", required=True)
    run.add_argument("--kind", required=True, choices=("test", "lint", "build", "security", "canary"))
    run.add_argument("--cwd", default=".")
    run.add_argument("command", nargs=argparse.REMAINDER)
    run.set_defaults(func=command_run)
    verify = sub.add_parser("verify", help="verifica evidencia citada por un veredicto")
    verify.add_argument("--task-dir", required=True)
    verify.add_argument("--repo", required=True)
    verify.add_argument("--commit", required=True)
    verify.add_argument("--verdict", required=True)
    verify.add_argument("--require-kind", action="append", default=[])
    verify.set_defaults(func=command_verify)
    return root


if __name__ == "__main__":
    ns = parser().parse_args()
    raise SystemExit(ns.func(ns))
