"""klanker-voice runner entrypoint (D-08 web verification surface).

Run modes::

    uv run python bot.py -t webrtc   # localhost page (pipecat-ai-prebuilt) at :7860
    uv run python bot.py -t eval     # eval-harness target (plan 01-03)

The webrtc mode serves the bundled prebuilt UI over the same SmallWebRTC
transport path as prod. Config comes from pipeline.toml (or the file named by
KLANKER_PIPELINE_CONFIG); API keys come from apps/voice/.env, written by
`make -C apps/voice env`.
"""

from dotenv import load_dotenv

from pipecat.evals.transport import EvalTransportParams
from pipecat.runner.types import RunnerArguments
from pipecat.runner.utils import create_transport
from pipecat.transports.base_transport import TransportParams
from pipecat.workers.runner import WorkerRunner

from klanker_voice.config import load_config
from klanker_voice.observers import LatencyReportObserver
from klanker_voice.pipeline import build_pipeline, build_worker, register_greet_first

load_dotenv(override=True)

transport_params = {
    "webrtc": lambda: TransportParams(audio_in_enabled=True, audio_out_enabled=True),
    "eval": lambda: EvalTransportParams(audio_in_enabled=True, audio_out_enabled=True),
}


async def bot(runner_args: RunnerArguments):
    """Per-session bot entry point, discovered and invoked by the pipecat runner."""
    transport = await create_transport(runner_args, transport_params)

    cfg = load_config()  # KLANKER_PIPELINE_CONFIG-aware
    built = build_pipeline(cfg, transport)
    # Every session is measured (D-11): JSON artifact in artifacts/harness/
    # plus a console table at session end, with zero extra flags.
    worker = build_worker(built.pipeline, observers=[LatencyReportObserver(cfg)])
    register_greet_first(transport, worker)

    runner = WorkerRunner(handle_sigint=runner_args.handle_sigint)
    await runner.add_workers(worker)
    await runner.run()


if __name__ == "__main__":
    from pipecat.runner.run import main

    main()
