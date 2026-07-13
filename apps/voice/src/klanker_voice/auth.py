"""Offline JWT validation for the Phase-3 access-token contract (T-04-01).

Every ``POST /api/offer`` request is gated by :func:`validate_access_token`
before any WebRTC transport is created. Validation is fully offline (the
JWKS is fetched once per process and cached by :class:`jwt.PyJWKClient`): an
RS256 signature check against the signing key named by the token's ``kid``,
then issuer/audience/exp checks, then the two namespaced tier/group claims
pinned by 03-03-SUMMARY.md.

A recognized smoke/service credential (``KMV_SMOKE_SERVICE_TOKEN``) bypasses
the JWKS round-trip entirely and marks the session ``bypass_accounting=True``
(D-15) — the KV-05 deployed smoke test's quota-free seam that 04-04's start
gate must honor explicitly, never implicitly.

Token bodies and the smoke credential value are never logged (T-04-02) —
only exception class names surface in the terse :class:`AuthError` reason.
"""

from __future__ import annotations

import hmac
import os
from dataclasses import dataclass
from functools import lru_cache

import jwt
from jwt import PyJWKClient

#: Pinned Phase-3 contract (03-03-SUMMARY.md "Phase-4 Contract — pinned
#: verbatim"). Overridable via env for staging/tests without touching code.
DEFAULT_ISSUER = "https://auth.klankermaker.ai/use1/api/oidc"
DEFAULT_JWKS_URI = "https://auth.klankermaker.ai/use1/api/oidc/jwks"
DEFAULT_AUDIENCE = "https://voice.klankermaker.ai"

ISSUER_ENV_VAR = "KMV_OIDC_ISSUER"
JWKS_URI_ENV_VAR = "KMV_OIDC_JWKS_URI"
AUDIENCE_ENV_VAR = "KMV_VOICE_AUDIENCE"
SMOKE_TOKEN_ENV_VAR = "KMV_SMOKE_SERVICE_TOKEN"

#: Namespaced claims from the Phase-3 contract (03-03-SUMMARY.md).
TIER_ID_CLAIM = "https://klankermaker.ai/tier_id"
GROUP_CLAIM = "https://klankermaker.ai/group"

#: Namespaced email/code claims added by Plan 15-01 (LEDG-01) for the
#: transcript ledger. These names MUST match `config.oidc.claimNames.email`
#: / `.code` in the auth app (apps/auth/webapp/src/config/index.ts)
#: byte-for-byte — the same cross-service contract discipline as the pinned
#: tier/group pair above.
EMAIL_CLAIM = "https://klankermaker.ai/email"
CODE_CLAIM = "https://klankermaker.ai/code"

#: Default tier when the claim is absent — matches the auth service's own
#: no-access default (03-03-SUMMARY.md D3).
NO_ACCESS_TIER_ID = "no-access"

#: Synthetic subject for the service/smoke credential path (D-15) — never a
#: real user id, so it can never collide with a real ``sub`` claim.
SMOKE_SERVICE_SUB = "service:smoke"


class AuthError(Exception):
    """Raised when a presented credential fails validation.

    Carries a terse, non-sensitive reason only — never the token or claim
    material (T-04-02).
    """


@dataclass(frozen=True)
class SessionIdentity:
    """The result of successfully validating a request's credential."""

    sub: str
    tier_id: str
    group: str | None
    bypass_accounting: bool = False
    #: Additive (LEDG-01, Plan 15-01/15-02): the ledger's identity fields,
    #: read from the token when present. Defaulted None so every existing
    #: constructor call (including the smoke/service path) stays unchanged.
    email: str | None = None
    code: str | None = None


def _issuer() -> str:
    return os.environ.get(ISSUER_ENV_VAR, DEFAULT_ISSUER)


def _jwks_uri() -> str:
    return os.environ.get(JWKS_URI_ENV_VAR, DEFAULT_JWKS_URI)


def _audience() -> str:
    return os.environ.get(AUDIENCE_ENV_VAR, DEFAULT_AUDIENCE)


@lru_cache(maxsize=1)
def _jwk_client() -> PyJWKClient:
    """Cached ``PyJWKClient`` — one JWKS fetch (+ its own internal cache) per
    process, keyed off the URI read at first call.

    Tests monkeypatch this function directly (``auth._jwk_client``) so
    validation stays fully offline and deterministic — no real network call
    is ever made in the unit suite.
    """
    return PyJWKClient(_jwks_uri())


def recognize_service_credential(token: str) -> bool:
    """Return True iff ``token`` matches the configured smoke/service credential.

    Constant-time compare (T-04-05) against ``KMV_SMOKE_SERVICE_TOKEN``. An
    unset/empty configured value or an empty token never matches.
    """
    configured = os.environ.get(SMOKE_TOKEN_ENV_VAR, "")
    if not configured or not token:
        return False
    return hmac.compare_digest(configured, token)


def validate_access_token(token: str) -> SessionIdentity:
    """Validate a bearer token per the Phase-3 contract, fully offline.

    The service/smoke credential (see :func:`recognize_service_credential`)
    is checked first and, on a match, short-circuits before any JWKS lookup.
    Otherwise this performs full RS256 verification: signing key resolved by
    the token's ``kid``, issuer/audience/exp all checked.

    Args:
        token: The raw bearer token string (may be empty/None-ish).

    Returns:
        A :class:`SessionIdentity` for the validated (or smoke) credential.

    Raises:
        AuthError: for any missing/malformed/expired/wrong-audience/
            wrong-issuer/unknown-signing-key credential.
    """
    if not token:
        raise AuthError("missing credential")

    if recognize_service_credential(token):
        return SessionIdentity(
            sub=SMOKE_SERVICE_SUB,
            tier_id=NO_ACCESS_TIER_ID,
            group=None,
            bypass_accounting=True,
        )

    try:
        signing_key = _jwk_client().get_signing_key_from_jwt(token)
        claims = jwt.decode(
            token,
            signing_key.key,
            algorithms=["RS256"],
            audience=_audience(),
            issuer=_issuer(),
            options={"require": ["exp", "iss", "aud"], "verify_exp": True},
        )
    except jwt.PyJWTError as exc:
        # Covers expired/bad-audience/bad-issuer/decode errors AND
        # PyJWKClientError (unknown kid / signing-key fetch failure) — both
        # are PyJWTError subclasses in PyJWT 2.13. Terse reason only.
        raise AuthError(f"token validation failed: {exc.__class__.__name__}") from exc

    sub = str(claims.get("sub") or "")
    tier_id = str(claims.get(TIER_ID_CLAIM) or NO_ACCESS_TIER_ID)
    group = claims.get(GROUP_CLAIM)
    email = claims.get(EMAIL_CLAIM)
    code = claims.get(CODE_CLAIM)
    return SessionIdentity(
        sub=sub,
        tier_id=tier_id,
        group=group,
        bypass_accounting=False,
        email=email,
        code=code,
    )
