"""StaticFiles SPA mount tests (05-01, D-01/02/03, CLNT-08).

Builds a throwaway FastAPI app (never the shared ``server.app`` singleton)
so mounting a fixture ``dist/`` here can never leak routes/exception
handlers into sibling test modules that import ``server`` and share its
app instance (test_server.py, test_smoke.py).
"""

from __future__ import annotations

from pathlib import Path

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

import server


def _make_app_with_mount(dist_dir: Path) -> FastAPI:
    app = FastAPI()

    @app.get("/health")
    async def health() -> dict[str, str]:
        return {"status": "ok"}

    @app.post("/api/offer")
    async def offer() -> dict[str, str]:
        return {"sdp": "v=0...", "type": "answer"}

    server._mount_client_spa(app, dist_dir)
    return app


@pytest.fixture
def dist_dir(tmp_path: Path) -> Path:
    d = tmp_path / "dist"
    d.mkdir()
    (d / "index.html").write_text("<html><body>klanker-voice SPA</body></html>", encoding="utf-8")
    return d


def test_root_serves_index_html(dist_dir: Path):
    client = TestClient(_make_app_with_mount(dist_dir))

    response = client.get("/")

    assert response.status_code == 200
    assert "klanker-voice SPA" in response.text


def test_deep_link_callback_falls_back_to_index_html(dist_dir: Path):
    """The OIDC authorization-code+PKCE callback route (D-04) is a
    client-side route with no matching file on disk — it must still resolve
    to index.html so the SPA router receives it."""
    client = TestClient(_make_app_with_mount(dist_dir))

    response = client.get("/callback")

    assert response.status_code == 200
    assert "klanker-voice SPA" in response.text


def test_health_and_api_offer_routes_are_not_shadowed(dist_dir: Path):
    client = TestClient(_make_app_with_mount(dist_dir))

    health_response = client.get("/health")
    offer_response = client.post("/api/offer")

    assert health_response.status_code == 200
    assert health_response.json() == {"status": "ok"}
    assert offer_response.status_code == 200
    assert offer_response.json() == {"sdp": "v=0...", "type": "answer"}


def test_unknown_api_path_404s_instead_of_falling_back_to_spa(dist_dir: Path):
    client = TestClient(_make_app_with_mount(dist_dir))

    response = client.get("/api/does-not-exist")

    assert response.status_code == 404
    assert "klanker-voice SPA" not in response.text


def test_hashed_asset_is_served_directly_not_as_index_html(dist_dir: Path):
    assets_dir = dist_dir / "assets"
    assets_dir.mkdir()
    (assets_dir / "main.abc123.js").write_text("console.log('hi');", encoding="utf-8")

    client = TestClient(_make_app_with_mount(dist_dir))

    response = client.get("/assets/main.abc123.js")

    assert response.status_code == 200
    assert response.text == "console.log('hi');"


def test_missing_dist_dir_skips_mount_without_crashing(tmp_path: Path):
    app = FastAPI()
    missing = tmp_path / "does-not-exist"

    server._mount_client_spa(app, missing)  # must not raise

    client = TestClient(app)
    response = client.get("/")

    assert response.status_code == 404
