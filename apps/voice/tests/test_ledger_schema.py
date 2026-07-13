"""Schema-drift guard (Pitfall 6, T-15-04-05): the Athena/Glue table's column
set declared in ``infra/terraform/modules/ledger/v1.0.0/main.tf`` MUST equal
``klanker_voice.ledger.LEDGER_FIELDS`` exactly (order and type both matter for
partition-projection queries reading real newline-JSON records). If either
side drifts silently, Athena queries would return null columns instead of
failing loudly — this test is the loud failure.

No AWS/terraform tooling required: a plain regex scan of the ``columns { ... }``
blocks inside the ``aws_glue_catalog_table`` resource. Both sides are also
compared against one hardcoded canonical list, so a change to *either* the
Terraform DDL or ``ledger.LEDGER_FIELDS`` alone (without updating the other)
fails this test.
"""

from __future__ import annotations

import re
from pathlib import Path

from klanker_voice import ledger

#: apps/voice/tests -> apps/voice -> repo root.
REPO_ROOT = Path(__file__).resolve().parent.parent.parent.parent

MAIN_TF_PATH = (
    REPO_ROOT / "infra" / "terraform" / "modules" / "ledger" / "v1.0.0" / "main.tf"
)

#: The canonical field set, hardcoded here too (not just imported from
#: ledger.py) so a drift in EITHER file — the Terraform DDL or the Python
#: tuple — fails loudly instead of the test silently comparing a moved
#: target against itself.
CANONICAL_FIELDS = (
    "role",
    "text",
    "email",
    "caller_id",
    "did",
    "ts",
    "session_id",
    "turn_seq",
    "code_hash",
    "tier_id",
    "channel",
    "interrupted",
)

#: Column name -> expected Glue/Hive type for each canonical field.
CANONICAL_TYPES = {
    "role": "string",
    "text": "string",
    "email": "string",
    "caller_id": "string",
    "did": "string",
    "ts": "bigint",
    "session_id": "string",
    "turn_seq": "int",
    "code_hash": "string",
    "tier_id": "string",
    "channel": "string",
    "interrupted": "boolean",
}

#: Matches one `columns { name = "x" type = "y" }` block inside the
#: storage_descriptor of aws_glue_catalog_table. Order-preserving.
_COLUMN_BLOCK_RE = re.compile(
    r'columns\s*\{\s*name\s*=\s*"([a-zA-Z0-9_]+)"\s*type\s*=\s*"([a-zA-Z0-9]+)"\s*\}',
    re.MULTILINE,
)


def _read_main_tf() -> str:
    assert MAIN_TF_PATH.exists(), f"expected ledger module main.tf at {MAIN_TF_PATH}"
    return MAIN_TF_PATH.read_text(encoding="utf-8")


def _extract_glue_columns() -> list[tuple[str, str]]:
    """Return [(name, type), ...] in file order, from the aws_glue_catalog_table
    resource's storage_descriptor columns blocks. Excludes the `dt` partition
    key (declared via a separate `partition_keys` block, not `columns`)."""
    text = _read_main_tf()
    return _COLUMN_BLOCK_RE.findall(text)


def test_canonical_fields_match_ledger_module_constant():
    """Sanity: the canonical list hardcoded in THIS test must equal
    ledger.LEDGER_FIELDS — if someone updates one without the other, fail
    loudly here before even touching the Terraform file."""
    assert CANONICAL_FIELDS == ledger.LEDGER_FIELDS
    assert set(CANONICAL_TYPES) == set(CANONICAL_FIELDS)


def test_glue_ddl_columns_equal_canonical_fields_in_order():
    """The Glue table's column names, in DDL order, must exactly equal the
    canonical field order — not just the same set (order matters for anyone
    reading raw JSONL by position, and for reviewer sanity)."""
    columns = _extract_glue_columns()
    assert columns, "no `columns { name = ... type = ... }` blocks found in main.tf"

    names = tuple(name for name, _type in columns)
    assert names == CANONICAL_FIELDS


def test_glue_ddl_column_types_match_canonical_types():
    """Each column's declared Glue/Hive type must match the canonical type
    map (e.g. `ts` must stay `bigint`, `turn_seq` must stay `int`,
    `interrupted` must stay `boolean` — not silently downgraded to `string`)."""
    columns = _extract_glue_columns()
    for name, glue_type in columns:
        assert glue_type == CANONICAL_TYPES[name], (
            f"column {name!r} has type {glue_type!r} in main.tf, "
            f"expected {CANONICAL_TYPES[name]!r}"
        )


def test_glue_ddl_excludes_the_dt_partition_from_columns():
    """`dt` is a partition key (declared via `partition_keys { name = "dt" ...
    }`), not a regular column — asserting its absence here catches a future
    accidental duplication into the columns list."""
    columns = _extract_glue_columns()
    names = {name for name, _type in columns}
    assert "dt" not in names


def test_main_tf_declares_a_dt_partition_key():
    """The table must still be partitioned by dt (partition projection relies
    on this) — a regression here would silently turn the table unpartitioned."""
    text = _read_main_tf()
    assert re.search(r'partition_keys\s*\{\s*name\s*=\s*"dt"', text)


def test_main_tf_declares_partition_projection():
    """Partition-projection TBLPROPERTIES must stay present (Pattern 5) — no
    MSCK REPAIR / crawler maintenance."""
    text = _read_main_tf()
    assert '"projection.enabled"' in text
    assert '"true"' in text
    assert "storage.location.template" in text
