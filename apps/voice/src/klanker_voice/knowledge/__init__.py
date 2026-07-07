"""Phase 7 knowledge subsystem: router + cached two-block system prompt.

``prompt_assembly`` builds the stable-prefix + swappable-pack ``system``
array (D-13); ``router`` classifies utterances and swaps the pack; ``lint``
is the advisory (flag-only, never-blocking) do-not-say checker for the
offline refresh workflow (Amendment 3-E).
"""

from __future__ import annotations
