#!/usr/bin/env python3
"""Generate and measure the fixed PR3 semantic extraction workspace.

Usage:
  python3 scripts/measure-semantic-extraction.py --lintai /tmp/lintai-pr2
  python3 scripts/measure-semantic-extraction.py --lintai /tmp/lintai-pr3 --source-links

The first command measures ordinary extraction. The second measures ordinary
extraction with one external declaration/source-entry association. Both use
byte-identical generated files. Build the lintai binary before invoking this
script so build time is not part of the extraction measurement.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import platform
import re
import shlex
import subprocess
import sys
import tempfile
import time
from pathlib import Path


FIXTURE = {
    "package.json": '{"name":"semantic-cost-fixture","private":true,"type":"module"}\n',
    "tsconfig.json": (
        '{\n'
        '  "compilerOptions": {\n'
        '    "target": "ES2022",\n'
        '    "module": "NodeNext",\n'
        '    "moduleResolution": "NodeNext",\n'
        '    "strict": true,\n'
        '    "skipLibCheck": true,\n'
        '    "noEmit": true\n'
        '  },\n'
        '  "include": ["src/**/*.ts"],\n'
        '  "exclude": ["node_modules", "packages"]\n'
        '}\n'
    ),
    "src/index.ts": (
        'export type { ValidationContext } from "./common.js";\n'
        'export type { EventVersion } from "./event.js";\n'
        'export type { EchoMessage } from "./echo.js";\n'
        'export type { CheckedClient } from "@fixture/checked";\n'
    ),
    "src/common.ts": 'export type { ValidationContext } from "./validation/index.js";\n',
    "src/validation/index.ts": 'export type { ValidationContext } from "./context.js";\n',
    "src/validation/context.ts": (
        'import type { Knex } from "knex";\n'
        'export type ValidationContext = {\n'
        '  db: Knex;\n'
        '  traceId: string;\n'
        '};\n'
    ),
    "src/event.ts": 'export interface EventVersion { eventVersionId: number }\n',
    "src/echo.ts": (
        'import type { EchoRequest } from "@echo/grpc";\n'
        'export type EchoMessage = EchoRequest;\n'
    ),
    "packages/checked/tsconfig.json": (
        '{\n'
        '  "compilerOptions": {\n'
        '    "target": "ES2022",\n'
        '    "module": "NodeNext",\n'
        '    "moduleResolution": "NodeNext",\n'
        '    "strict": true,\n'
        '    "skipLibCheck": true,\n'
        '    "noEmit": true\n'
        '  },\n'
        '  "include": ["src/index.ts"]\n'
        '}\n'
    ),
    "packages/checked/src/index.ts": (
        'export interface CheckedClient {\n'
        '  send(payload: string): Promise<string>;\n'
        '}\n'
    ),
    "node_modules/@fixture/checked/package.json": (
        '{"name":"@fixture/checked","types":"./dist/index.d.ts",'
        '"exports":{".":{"types":"./dist/index.d.ts"}}}\n'
    ),
    "node_modules/@fixture/checked/dist/index.d.ts": (
        'export interface CheckedClient {\n'
        '  send(payload: string): Promise<string>;\n'
        '}\n'
    ),
    "node_modules/knex/package.json": '{"name":"knex","types":"index.d.ts"}\n',
    "node_modules/knex/index.d.ts": (
        'export interface Knex {\n'
        '  query(sql: string): Promise<unknown>;\n'
        '}\n'
    ),
    "node_modules/@echo/grpc/package.json": (
        '{"name":"@echo/grpc","types":"index.d.ts"}\n'
    ),
    "node_modules/@echo/grpc/index.d.ts": (
        'export interface EchoRequest { message: string }\n'
    ),
    "source-links.json": (
        '{\n'
        '  "version": 1,\n'
        '  "entries": [{\n'
        '    "checkedEntry": "node_modules/@fixture/checked/dist/index.d.ts",\n'
        '    "sourceEntry": "packages/checked/src/index.ts",\n'
        '    "sourceTsconfig": "packages/checked/tsconfig.json"\n'
        '  }]\n'
        '}\n'
    ),
}

FACT_LINE = re.compile(r"^\s*([a-z][a-z_0-9]*)\(")


def write_fixture(root: Path) -> str:
    """Write fixed UTF-8 inputs and return a path-and-content SHA-256."""
    digest = hashlib.sha256()
    for name, content in sorted(FIXTURE.items()):
        path = root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        data = content.encode("utf-8")
        path.write_bytes(data)
        digest.update(name.encode("utf-8"))
        digest.update(b"\0")
        digest.update(data)
        digest.update(b"\0")
    return digest.hexdigest()


def run_measured(command: list[str], env: dict[str, str]) -> tuple[int, str, str, float, int]:
    """Run one child and collect its own wait4 resource usage."""
    # Files avoid pipe deadlock while wait4 waits for the complete extraction.
    with tempfile.TemporaryFile(mode="w+t", encoding="utf-8") as stdout, tempfile.TemporaryFile(
        mode="w+t", encoding="utf-8"
    ) as stderr:
        started = time.monotonic()
        process = subprocess.Popen(command, stdout=stdout, stderr=stderr, env=env)
        _, status, usage = os.wait4(process.pid, 0)
        elapsed = time.monotonic() - started
        process.returncode = os.waitstatus_to_exitcode(status)
        stdout.seek(0)
        stderr.seek(0)
        return process.returncode, stdout.read(), stderr.read(), elapsed, usage.ru_maxrss


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--lintai", required=True, type=Path, help="prebuilt lintai binary")
    parser.add_argument(
        "--source-links",
        action="store_true",
        help="measure the new-version association case with the fixture's source-links.json",
    )
    args = parser.parse_args()
    binary = args.lintai.resolve()
    if not binary.is_file():
        parser.error(f"lintai binary does not exist: {binary}")

    if not hasattr(os, "wait4") or platform.system() not in {"Darwin", "Linux"}:
        parser.error("this measurement requires wait4 on macOS or Linux")

    with tempfile.TemporaryDirectory(prefix="lintai-semantic-cost-") as temporary:
        workspace = Path(temporary) / "workspace"
        workspace.mkdir()
        digest = write_fixture(workspace)
        command = [
            str(binary),
            "facts",
            "--workspace-root",
            str(workspace),
            "--exclude",
            "packages/checked/**",
        ]
        if args.source_links:
            command.extend(["--source-links", str(workspace / "source-links.json")])

        env = os.environ.copy()
        env["LINTAI_EXTRACT_TIMING"] = "1"
        print(f"mode: {'association' if args.source_links else 'ordinary'}")
        print(f"command: {shlex.join(command)}")
        print(
            "fixture: files=17 source_ts=7 declaration_d_ts=3 tsconfigs=2 "
            "package_json=4 source_entries=1; selected_roots exclude packages/checked/**"
        )
        print(f"fixture_sha256: {digest}")

        returncode, stdout, stderr, elapsed, peak_rss = run_measured(command, env)
        print(f"lintai_exit: {returncode}")
        if returncode != 0:
            print("measurement_status: failed; lintai exited unsuccessfully")
            if stderr:
                sys.stderr.write(stderr)
            return returncode
        relation_counts: dict[str, int] = {}
        if stdout:
            fact_rows = []
            for line in stdout.splitlines():
                match = FACT_LINE.match(line)
                if match:
                    fact_rows.append((match.group(1), line))
            for relation, _ in fact_rows:
                relation_counts[relation] = relation_counts.get(relation, 0) + 1
            type_rows = sum(count for relation, count in relation_counts.items() if relation.startswith("type_"))
            type_entities = relation_counts.get("type_kind", 0)
            declaration_rows = sum(
                count for relation, count in relation_counts.items() if relation.startswith("declaration")
            )
            export_rows = sum(
                count
                for relation, count in relation_counts.items()
                if relation == "module_export" or relation.startswith("export_")
            )
            print(f"facts: total={len(fact_rows)} type_relation_rows={type_rows} type_entities={type_entities}")
            if declaration_rows:
                print(f"facts: declaration_relation_rows={declaration_rows}")
            else:
                print("facts: declaration_relation_rows=0 (not emitted by this revision)")
            if export_rows:
                print(f"facts: export_relation_rows={export_rows}")
            else:
                print("facts: export_relation_rows=0 (not emitted by this revision)")

        print(f"elapsed_wall: {elapsed:.3f}s")
        rss_bytes = peak_rss if platform.system() == "Darwin" else peak_rss * 1024
        print(f"peak_rss: {rss_bytes / (1024 * 1024):.1f} MiB (wait4)")
        for line in stderr.splitlines():
            if "lintai: extract " in line:
                print(f"extract_timing: {line.strip()}")
        valid = bool(relation_counts)
        if args.source_links:
            timing_lines = [line for line in stderr.splitlines() if "lintai: extract " in line]
            detail = " ".join(timing_lines)
            count = re.search(r"(?:auxiliary|aux)_programs?=(\d+)", detail)
            millis = re.search(r"(?:auxiliary|aux)_program_ms=(\d+)", detail)
            if count and millis:
                print(f"auxiliary_programs: count={count.group(1)} time={millis.group(1)}ms")
            else:
                print("auxiliary_programs: count/time not emitted in LINTAI_EXTRACT_TIMING output")
            expected = {
                "source_entry_applied": 1,
                "source_entry_unapplied": 0,
                "source_entry_incomplete": 0,
                "source_binding_attempt": 1,
                "source_binding": 1,
                "source_binding_checked_decl": 1,
                "source_binding_source_decl": 1,
                "source_binding_incomplete": 0,
            }
            print("association_facts: " + " ".join(f"{name}={relation_counts.get(name, 0)}" for name in expected))
            valid = valid and count is not None and int(count.group(1)) == 1 and millis is not None
            valid = valid and all(relation_counts.get(name, 0) == expected_count for name, expected_count in expected.items())
        if not valid:
            print("measurement_status: failed; fixture did not exercise the expected extraction")
            return 1
        print("measurement_status: ok")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
