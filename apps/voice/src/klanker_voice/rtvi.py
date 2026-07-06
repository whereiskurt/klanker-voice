"""RTVIProcessor + RTVIObserverParams factories (CLNT-03/04/06, D-09).

Standard 1.5.0 RTVI wiring gives client-js transcripts (user + bot), bot/user
speaking events, and metrics for free once an ``RTVIProcessor`` sits in the
pipeline and an ``RTVIObserver`` is attached to the worker — no custom frame
handling required (RESEARCH Don't Hand-Roll). This module is the single place
that builds both halves of the pair so ``pipeline.py`` (placement) and
``server.py`` (worker wiring) share one construction path.

The one thing NOT covered by the observer-params defaults: audio levels for
the orb's amplitude-driven deformation (D-06) — ``bot_audio_level_enabled``
and ``user_audio_level_enabled`` both default False upstream, so
:func:`build_rtvi_observer_params` flips them on explicitly. Everything else
(``bot_speaking_enabled``, ``user_transcription_enabled``, ``metrics_enabled``)
is already True by default.
"""

from __future__ import annotations

from pipecat.processors.frameworks.rtvi import RTVIObserverParams, RTVIProcessor


def build_rtvi_processor() -> RTVIProcessor:
    """Construct a fresh RTVIProcessor for one session's pipeline.

    One instance per session (mirrors every other per-session service built in
    ``build_pipeline``) — never shared across connections.
    """
    return RTVIProcessor()


def build_rtvi_observer_params() -> RTVIObserverParams:
    """RTVIObserverParams for the orb + captions + HUD (CLNT-03/04/06, D-06).

    Enables ``bot_audio_level_enabled`` and ``user_audio_level_enabled`` on
    top of the library defaults so the orb gets amplitude while the user is
    talking (mic RMS) and while the agent is talking (TTS output RMS).
    """
    return RTVIObserverParams(
        bot_audio_level_enabled=True,
        user_audio_level_enabled=True,
    )
