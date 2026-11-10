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


def parse_peak_rss(stderr: str) -> tuple[float | None, str | None]:
    """Parse /usr/bin/time's macOS or GNU maximum-resident-set-size field."""
    mac = re.search(r"^\s*(\d+)\s+maximum resident set size\s*$", stderr, re.MULTILINE)
    if mac:
        return int(mac.group(1)) / (1024 * 1024), "MiB"
    gnu = re.search(r"Maximum resident set size \(kbytes\):\s*(\d+)", stderr)
    if gnu:
        return int(gnu.group(1)) / 1024, "MiB"
    return None, None


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

    timer = Path("/usr/bin/time")
    if not timer.exists():
        parser.error("/usr/bin/time is required to collect peak RSS")
    timer_args = ["-l"] if platform.system() == "Darwin" else ["-v"]

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
        full_command = [str(timer), *timer_args, *command]
        print(f"mode: {'association' if args.source_links else 'ordinary'}")
        print(f"command: {shlex.join(command)}")
        print(
            "fixture: files=17 source_ts=7 declaration_d_ts=3 tsconfigs=2 "
            "package_json=4 source_entries=1; selected_roots exclude packages/checked/**"
        )
        print(f"fixture_sha256: {digest}")

        started = time.monotonic()
        result = subprocess.run(full_command, capture_output=True, text=True, env=env)
        elapsed = time.monotonic() - started
        host_command = (
            f"python3 scripts/measure-semantic-extraction.py --lintai {shlex.quote(str(binary))}"
            + (" --source-links" if args.source_links else "")
        )
        if result.returncode != 0:
            print(f"measurement_status: failed; /usr/bin/time exited {result.returncode}")
            if "sysctl kern.clockrate: Operation not permitted" in result.stderr:
                print("peak_rss: unavailable; sandbox blocked /usr/bin/time sysctl query")
            print(f"host_rss_command: {host_command}")
            if result.stderr:
                sys.stderr.write(result.stderr)
            return result.returncode
        if result.stdout:
            fact_rows = []
            for line in result.stdout.splitlines():
                match = FACT_LINE.match(line)
                if match:
                    fact_rows.append((match.group(1), line))
            relation_counts: dict[str, int] = {}
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

        rss, rss_unit = parse_peak_rss(result.stderr)
        print(f"elapsed_wall: {elapsed:.3f}s")
        if rss is None:
            print("peak_rss: unavailable; /usr/bin/time output had no RSS field")
            print(f"host_rss_command: {host_command}")
        else:
            print(f"peak_rss: {rss:.1f} {rss_unit}")
        for line in result.stderr.splitlines():
            if "lintai: extract " in line:
                print(f"extract_timing: {line.strip()}")
        if args.source_links:
            timing_lines = [line for line in result.stderr.splitlines() if "lintai: extract " in line]
            detail = " ".join(timing_lines)
            count = re.search(r"(?:auxiliary|aux)_programs?=(\d+)", detail)
            millis = re.search(r"(?:auxiliary|aux)_program_ms=(\d+)", detail)
            if count and millis:
                print(f"auxiliary_programs: count={count.group(1)} time={millis.group(1)}ms")
            else:
                print("auxiliary_programs: count/time not emitted in LINTAI_EXTRACT_TIMING output")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
