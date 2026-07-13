## Summary

<!-- 1-3 sentence description of what this PR does and why. -->

## Changes

<!-- Bullet list of the meaningful changes. -->

-

## Testing

<!-- How did you verify this works? -->

- [ ] `uv run pytest` passes in `apps/voice/` (if the pipeline changed)
- [ ] `npm test` / `npm run build` pass in `apps/voice/client/` and/or `apps/auth/webapp/` (if the web apps changed)
- [ ] `go test ./...` passes in `kv/` (if the CLI changed)
- [ ] Verified by ear against a live or local session where the change affects conversation behavior (latency, barge-in, greeting, speech quality)

## Documentation

- [ ] `README.md` and/or relevant `docs/` page updated for any new config knob, pipeline stage, endpoint, or AWS resource

## DCO sign-off

Every commit must include a `Signed-off-by:` line. Use `git commit -s` to add it automatically.

By signing off I confirm I have the right to contribute this work under the [MIT License](../LICENSE) and have read the contributor warranty in [CONTRIBUTING.md](CONTRIBUTING.md), including the employer / third-party IP terms.

- [ ] All commits are signed off

## Related issues

<!-- Closes #N, refs #M -->
