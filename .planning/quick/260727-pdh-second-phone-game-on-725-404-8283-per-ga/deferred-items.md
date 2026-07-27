# Deferred Items — quick task 260727-pdh

## Pre-existing gofmt drift, out of scope

`cd kv && gofmt -l .` reports two files as not gofmt-clean:

- `kv/internal/app/cmd/code.go`
- `kv/internal/app/cmd/voipms.go`

Both predate this task (last touched by quick task 260721-te5, commit
`fc90763`) and are untouched by any commit in this task. Per the executor's
scope-boundary rule ("only auto-fix issues directly caused by the current
task's changes"), these are logged here rather than fixed.

Every file this task's Task 3 actually touched (`studio/types.go`,
`studio/repofile_adapter.go`, `studio/view.go`, `studio/server.go`,
`studio/repofile_adapter_test.go`, `studio/view_test.go`,
`cmd/telephony.go`, `cmd/telephony_test.go`) is gofmt-clean, verified via
`gofmt -l <touched files>` before each commit.

Note for the plan's own literal Task 3 `<verify>` command
(`gofmt -l . | tee /dev/stderr | wc -l | grep -qx '[[:space:]]*0'`): this
exact command will report `2`, not `0`, on this branch because of the
pre-existing drift above — not because of anything this task introduced.
