"""Server/pipeline build stamp.

The short git SHA (+ UTC build time) the running voice image was built from,
injected as ``APP_VERSION`` / ``APP_BUILT_AT`` env at ``docker build`` time
(Dockerfile python stage ARG/ENV, same commit as the client's VITE_APP_VERSION).
Returned in the ``/api/offer`` answer so the UI can show ``pipe:<sha>`` next to
its own ``ui:<sha>`` — confirming the ECS pipeline (not just the CloudFront
assets) rolled to this commit. Falls back to ``"dev"`` for local runs.
"""
from __future__ import annotations

import os

#: Short commit SHA of the running image (or "dev" locally).
APP_VERSION: str = os.environ.get("APP_VERSION") or "dev"
#: UTC build timestamp of the running image (or "" locally).
APP_BUILT_AT: str = os.environ.get("APP_BUILT_AT") or ""
