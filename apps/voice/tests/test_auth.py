"""Offline RS256 validation + service-credential recognition (T-04-01, T-04-05).

Every test mints tokens against a locally generated RSA keypair and
monkeypatches ``auth._jwk_client`` with an in-memory fake keyed by ``kid`` —
no network call is ever made (fake keys, not real cryptographic weakness).
"""

from __future__ import annotations

import time

import jwt
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa

from klanker_voice import auth

ISSUER = auth.DEFAULT_ISSUER
AUDIENCE = auth.DEFAULT_AUDIENCE
KID = "test-kid-1"
OTHER_KID = "unknown-kid"


@pytest.fixture(scope="module")
def keypair():
    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    return private_key, private_key.public_key()


@pytest.fixture(scope="module")
def other_keypair():
    private_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    return private_key, private_key.public_key()


class FakeSigningKey:
    """Stand-in for jwt.PyJWK — exposes just the ``.key`` attribute jwt.decode reads."""

    def __init__(self, key):
        self.key = key


class FakeJWKClient:
    """In-memory JWKS: kid -> public key. Raises like PyJWKClient on a miss."""

    def __init__(self, keys_by_kid: dict[str, object]):
        self._keys_by_kid = keys_by_kid
        self.calls = 0

    def get_signing_key_from_jwt(self, token: str) -> FakeSigningKey:
        self.calls += 1
        header = jwt.get_unverified_header(token)
        kid = header.get("kid")
        if kid not in self._keys_by_kid:
            raise jwt.PyJWKClientError(f"Unable to find a signing key for kid: {kid!r}")
        return FakeSigningKey(self._keys_by_kid[kid])


def mint_token(
    private_key,
    *,
    kid: str = KID,
    issuer: str = ISSUER,
    audience: str = AUDIENCE,
    exp_delta: int = 3600,
    extra_claims: dict | None = None,
) -> str:
    now = int(time.time())
    claims = {
        "sub": "user-123",
        "iss": issuer,
        "aud": audience,
        "iat": now,
        "exp": now + exp_delta,
        "scope": "voice",
    }
    claims.update(extra_claims or {})
    return jwt.encode(claims, private_key, algorithm="RS256", headers={"kid": kid})


@pytest.fixture(autouse=True)
def offline_jwks(monkeypatch, keypair):
    """Point auth._jwk_client() at a fake, offline JWKS keyed by KID by default."""
    _, public_key = keypair
    fake = FakeJWKClient({KID: public_key})
    monkeypatch.setattr(auth, "_jwk_client", lambda: fake)
    return fake


@pytest.fixture(autouse=True)
def no_smoke_token(monkeypatch):
    """Ensure the smoke/service credential is unset unless a test opts in."""
    monkeypatch.delenv(auth.SMOKE_TOKEN_ENV_VAR, raising=False)


def test_valid_token_yields_session_identity(keypair):
    private_key, _ = keypair
    token = mint_token(
        private_key,
        extra_claims={
            auth.TIER_ID_CLAIM: "premium",
            auth.GROUP_CLAIM: "friends-and-family",
        },
    )

    identity = auth.validate_access_token(token)

    assert identity == auth.SessionIdentity(
        sub="user-123",
        tier_id="premium",
        group="friends-and-family",
        bypass_accounting=False,
    )


def test_missing_tier_id_claim_defaults_to_no_access(keypair):
    private_key, _ = keypair
    token = mint_token(private_key)

    identity = auth.validate_access_token(token)

    assert identity.tier_id == auth.NO_ACCESS_TIER_ID
    assert identity.bypass_accounting is False


def test_expired_token_raises_auth_error(keypair):
    private_key, _ = keypair
    token = mint_token(private_key, exp_delta=-60)

    with pytest.raises(auth.AuthError):
        auth.validate_access_token(token)


def test_wrong_audience_raises_auth_error(keypair):
    private_key, _ = keypair
    token = mint_token(private_key, audience="https://not-voice.klankermaker.ai")

    with pytest.raises(auth.AuthError):
        auth.validate_access_token(token)


def test_wrong_issuer_raises_auth_error(keypair):
    private_key, _ = keypair
    token = mint_token(private_key, issuer="https://evil.example.com/oidc")

    with pytest.raises(auth.AuthError):
        auth.validate_access_token(token)


def test_unknown_signing_key_raises_auth_error(other_keypair):
    """Signed by a key whose kid never appears in the (fake) JWKS."""
    other_private_key, _ = other_keypair
    token = mint_token(other_private_key, kid=OTHER_KID)

    with pytest.raises(auth.AuthError):
        auth.validate_access_token(token)


def test_service_credential_bypasses_jwks_and_sets_bypass_accounting(monkeypatch, offline_jwks):
    monkeypatch.setenv(auth.SMOKE_TOKEN_ENV_VAR, "s3kr1t-smoke-credential")

    identity = auth.validate_access_token("s3kr1t-smoke-credential")

    assert identity == auth.SessionIdentity(
        sub=auth.SMOKE_SERVICE_SUB,
        tier_id=auth.NO_ACCESS_TIER_ID,
        group=None,
        bypass_accounting=True,
    )
    assert offline_jwks.calls == 0, "service credential must never hit the JWKS client"


def test_recognize_service_credential_constant_time_no_match(monkeypatch):
    monkeypatch.setenv(auth.SMOKE_TOKEN_ENV_VAR, "s3kr1t-smoke-credential")

    assert auth.recognize_service_credential("wrong-value") is False
    assert auth.recognize_service_credential("") is False


def test_recognize_service_credential_unset_never_matches(monkeypatch):
    monkeypatch.delenv(auth.SMOKE_TOKEN_ENV_VAR, raising=False)

    assert auth.recognize_service_credential("anything") is False


def test_missing_token_raises_auth_error():
    with pytest.raises(auth.AuthError):
        auth.validate_access_token("")
