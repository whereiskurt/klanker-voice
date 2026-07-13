# Deferred items — quick task 260713-m9n

## Pre-existing moto/DynamoDB test-environment failure (out of scope)

`tests/test_quota.py`, `tests/test_session.py`, `tests/test_slot_leak.py`,
`tests/test_teardown.py`, `tests/test_winddown.py` fail in this sandbox with
`botocore.errorfactory.ResourceNotFoundException: ... Cannot do operations on
a non-existent table` (the `fake_aws`/moto DynamoDB fixture is not creating
the table before the test body runs).

Confirmed unrelated to this plan's changes:
- None of these files were touched by this plan (only
  `telephony/pickup_cue.py`, `telephony/controller.py`,
  `render_pickup_cue.py`, the two new asset files, the Makefile, and the
  three new `test_pickup_cue_*`/`test_controller_pickup_cue.py` test files
  were touched).
- The failures reproduce running each file standalone
  (`pytest tests/test_quota.py -q`, `pytest tests/test_session.py -q`), with
  zero involvement from any file this plan added/modified.

Per the executor's scope-boundary rule (only auto-fix issues directly caused
by the current task's changes), this was logged, not fixed. All of this
plan's own tests (`test_pickup_cue_voice_drift.py`, `test_pickup_cue_player.py`,
`test_controller_pickup_cue.py`) and every `controller`/`telephony` test pass
cleanly (127/127), so this is an isolated, pre-existing environment gap, not a
regression introduced here.
