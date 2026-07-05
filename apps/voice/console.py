"""klanker-voice terminal mode: laptop mic/speaker via LocalAudioTransport (D-08).

The fast prompt-iteration surface. Runs the exact same load_config +
build_pipeline + greet-first path as bot.py — zero pipeline logic duplicated.

    uv run python console.py

Notes:
    * Wear HEADPHONES for barge-in testing at the terminal: laptop speakers
      feed bot audio back into the mic and the bot interrupts itself in a loop
      (echo self-interruption, RESEARCH Pitfall 6).
    * pyaudio (behind LocalAudioTransport) comes from the dev dependency group
      (`pipecat-ai[local]`), which needs brew's portaudio — installed in plan
      01-01. Prod images never carry it.
    * LocalAudioTransport has no client-connect event, so the greeting is
      queued directly at startup instead of via register_greet_first.
"""

import asyncio

from dotenv import load_dotenv

from pipecat.transports.local.audio import LocalAudioTransport, LocalAudioTransportParams
from pipecat.workers.runner import WorkerRunner

from klanker_voice.config import load_config
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import build_pipeline, build_worker, greet_now


async def main():
    load_dotenv(override=True)

    transport = LocalAudioTransport(
        LocalAudioTransportParams(audio_in_enabled=True, audio_out_enabled=True)
    )

    cfg = load_config()  # KLANKER_PIPELINE_CONFIG-aware
    built = build_pipeline(cfg, transport)
    # Terminal iteration gets numbers for free (D-11): JSON artifact +
    # console table at session end, no extra flags.
    worker = build_worker(built.pipeline, observers=[LatencyReportObserver(cfg)])

    runner = WorkerRunner(handle_sigint=True)
    await runner.add_workers(worker)
    # Terminal mode: no on_client_connected event — greet as soon as we start (D-04).
    await greet_now(worker, built.context)
    await runner.run()


if __name__ == "__main__":
    asyncio.run(main())
