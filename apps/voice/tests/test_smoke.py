"""Install smoke: the pinned pipecat 1.5.x tree and the klanker_voice package import."""


def test_pipecat_version_is_pinned_line():
    import pipecat

    assert pipecat.__version__.startswith("1.5.")


def test_klanker_voice_package_importable():
    import klanker_voice  # noqa: F401
