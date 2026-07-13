"""Standalone telephony ARI/Stasis controller entrypoint (Phase 11, D-08).

**Process boundary (mirrors the browser transport module / ``server.py``
isolation, see ``telephony/controller.py``'s own module docstring).** This
module is the *only* way the telephony surface is ever brought up as a
running process: its own local process, run alongside the docker-compose
Asterisk instance (``apps/voice/asterisk/docker-compose.yml``) and entirely
separate from the browser ``server.py`` FastAPI process. It constructs one
:class:`~klanker_voice.telephony.ari.AriClient`, wires
:class:`~klanker_voice.telephony.controller.AsteriskCallController`'s three
event handlers onto it, connects the events WebSocket, and runs the
dispatch loop -- **no FastAPI, no HTTP server of its own** (Asterisk's own
built-in HTTP server is what serves ARI; the Klanker side is a WebSocket +
REST client only). This module never imports, and is never imported by,
``server.py`` or the browser transport module (D-08).

Two equivalent, documented run commands (11-RESEARCH.md R6 -- either is
fine to pick as "the" entrypoint; both are wired here so neither reading of
D-08's own phrasing is wrong):

    python -m klanker_voice.telephony            # runs THIS module directly
    python -m klanker_voice.telephony.controller  # controller.py's own
                                                   # `if __name__ == "__main__"`
                                                   # guard imports and calls
                                                   # this module's main()

The harness README (``apps/voice/asterisk/README.md``) documents
``python -m klanker_voice.telephony.controller`` as the primary/canonical
command (it matches D-08's own literal phrasing and the module that owns
the ``AsteriskCallController``/``ActiveCall`` registry callers actually
care about).

Secrets (§13/D-09): ``ASTERISK_ARI_URL``/``ASTERISK_ARI_USERNAME``/
``ASTERISK_ARI_PASSWORD`` are read from the environment ONLY, here -- never
from ``pipeline.toml``/``configs/telephony.toml`` (config.py's shared
credential-field-name regex would refuse them there anyway). Never logged.
The §24 gate secrets (``TELEPHONY_ACCESS_PIN``/``TELEPHONY_PASSPHRASE_WORDS``)
are read separately, by ``AsteriskCallController`` itself, at construction.

Run with::

    cd apps/voice
    KLANKER_PIPELINE_CONFIG=configs/telephony.toml \\
    ASTERISK_ARI_URL=http://127.0.0.1:8088 \\
    ASTERISK_ARI_USERNAME=klanker \\
    ASTERISK_ARI_PASSWORD=... \\
    TELEPHONY_ACCESS_PIN=... \\
    TELEPHONY_PASSPHRASE_WORDS='w1 w2 w3 w4' \\
    uv run python -m klanker_voice.telephony.controller
"""

from __future__ import annotations

import asyncio
import os

from dotenv import load_dotenv
from loguru import logger

from klanker_voice import ledger
from klanker_voice.config import ConfigError, load_config, load_knowledge_config, load_quota_config
from klanker_voice.telephony.ari import AriClient
from klanker_voice.telephony.config import load_telephony_config
from klanker_voice.telephony.controller import DEFAULT_APP_NAME, AsteriskCallController

#: Phase 15 (LEDG-02): bounded well under a normal process-manager
#: SIGTERM->SIGKILL window -- mirrors server.py's LEDGER_DRAIN_TIMEOUT_SECONDS.
LEDGER_DRAIN_TIMEOUT_SECONDS = 10.0

#: ARI connection secrets (§13/D-09): env-only, never pipeline.toml.
ARI_URL_ENV_VAR = "ASTERISK_ARI_URL"
ARI_USERNAME_ENV_VAR = "ASTERISK_ARI_USERNAME"
ARI_PASSWORD_ENV_VAR = "ASTERISK_ARI_PASSWORD"

#: The dev-harness default (apps/voice/asterisk/docker-compose.yml publishes
#: 8088:8088 -- see that harness's README for the current loopback-bindaddr
#: caveat). Only used when ASTERISK_ARI_URL is unset.
DEFAULT_ARI_URL = "http://127.0.0.1:8088"


async def main() -> None:
    """Load config, guard on ``[telephony].enabled``, then run the ARI
    controller until its events WebSocket closes.

    This module has no dependency, direct or transitive, on the browser
    HTTP framework or the browser transport module -- see the module
    docstring's own D-08 process-boundary note.
    """
    load_dotenv(override=True)

    cfg = load_config()
    knowledge_cfg = load_knowledge_config()
    quota_cfg = load_quota_config()
    telephony_cfg = load_telephony_config()

    if not telephony_cfg.enabled:
        logger.info(
            "telephony.__main__: [telephony].enabled is false in the resolved pipeline "
            "config -- nothing to run. Select a harness config that enables it, e.g. "
            "KLANKER_PIPELINE_CONFIG=configs/telephony.toml."
        )
        return

    ari_url = os.environ.get(ARI_URL_ENV_VAR, DEFAULT_ARI_URL)
    ari_username = os.environ.get(ARI_USERNAME_ENV_VAR, "")
    ari_password = os.environ.get(ARI_PASSWORD_ENV_VAR, "")
    if not ari_username or not ari_password:
        raise ConfigError(
            f"{ARI_USERNAME_ENV_VAR} and {ARI_PASSWORD_ENV_VAR} must both be set (D-09: ARI "
            "credentials are env-only, never in pipeline.toml/configs/telephony.toml)"
        )

    ari = AriClient(ari_url, ari_username, ari_password, DEFAULT_APP_NAME)
    controller = AsteriskCallController(ari, cfg, knowledge_cfg, quota_cfg, telephony_cfg)
    controller.register()

    logger.info(
        f"telephony controller starting: ari_url={ari_url!r} app={DEFAULT_APP_NAME!r} "
        f"require_gate={telephony_cfg.require_gate} gate_mode={telephony_cfg.gate_mode!r}"
    )
    await ari.connect()
    try:
        await ari.run()
    finally:
        # Phase 15 (LEDG-02, Pitfall 3): drain every live call's buffered
        # ledger records before the ARI connection itself closes -- bounded
        # so a hung writer can never block process exit.
        await ledger.flush_all(timeout=LEDGER_DRAIN_TIMEOUT_SECONDS)
        await ari.close()
    logger.info("telephony controller: events WebSocket closed, exiting")


if __name__ == "__main__":
    asyncio.run(main())
