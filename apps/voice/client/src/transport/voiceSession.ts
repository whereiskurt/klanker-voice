import { PipecatClient, type APIRequest, type RTVIEventCallbacks } from "@pipecat-ai/client-js";
import { SmallWebRTCTransport } from "@pipecat-ai/small-webrtc-transport";
import type { ConnectionEvent, OfferRejection } from "./connectionState";

/** Same-origin, HTTPS in production -- server.py's only signaling contract. */
const OFFER_ENDPOINT = "/api/offer";

/**
 * Builds the `/api/offer` request the `SmallWebRTCTransport` uses for its
 * SDP offer/answer negotiation (CLNT-02, T-05-04-I). The Bearer token is
 * attached as an `Authorization` header -- `server.py`'s
 * `_extract_bearer_token()` checks this header FIRST, before falling back
 * to a `request_data.access_token` field the vendor client does not
 * populate in that exact shape (`small-webrtc-transport` 1.10.5 sends the
 * caller's `requestData` back to the server nested under the *camelCase*
 * key `requestData`, not the *snake_case* `request_data` server.py's
 * pre-gate token check reads -- see `SmallWebRTCRequest.from_dict`, which
 * only normalizes `requestData` -> `request_data` *after* the auth/gate
 * check has already run). The header is therefore the one reliable Bearer
 * path for this transport version; never placed in a URL/query, never
 * logged.
 */
export function buildConnectParams(token: string | null): APIRequest {
  const headers = new Headers();
  if (token) headers.set("Authorization", `Bearer ${token}`);
  return { endpoint: OFFER_ENDPOINT, headers };
}

function requestUrl(input: RequestInfo | URL): string {
  if (typeof input === "string") return input;
  if (input instanceof URL) return input.toString();
  return input.url;
}

function isOfferRequest(input: RequestInfo | URL, init: RequestInit | undefined): boolean {
  const method = (init?.method ?? "GET").toUpperCase();
  return method === "POST" && requestUrl(input).includes(OFFER_ENDPOINT);
}

async function parseRejection(response: Response): Promise<OfferRejection> {
  let body: { error?: string; message?: string } = {};
  try {
    body = (await response.clone().json()) as { error?: string; message?: string };
  } catch {
    // Non-JSON error body (shouldn't happen per server.py) -- status alone
    // is still an honest, distinct-from-connected outcome.
  }
  return { status: response.status, error: body.error, message: body.message };
}

export interface VoiceSession {
  /** The underlying RTVI client -- Task 3 subscribes orb/caption bindings
   * directly via `client.on(RTVIEvent.X, handler)`. */
  client: PipecatClient;
  connect: () => Promise<void>;
  disconnect: () => Promise<void>;
}

export interface CreateVoiceSessionOptions {
  getToken: () => string | null;
  /** Dispatched for connection-state-machine-relevant transitions. */
  onEvent: (event: ConnectionEvent) => void;
  /** Additional RTVI callbacks (transcripts, audio levels, speaking, etc.) --
   * merged with this module's own CONNECTED/DISCONNECTED/error wiring. */
  rtvi?: RTVIEventCallbacks;
  /** Fired once with the tier `session_max_seconds` the `/api/offer`
   * response carries (CLNT-05, D-10) -- the JWT itself only carries
   * `tier_id`, not the numeric cap, so the connect flow's own answer is the
   * one source for the client countdown (05-05 key_link). Display only. */
  onSessionMax?: (sessionMaxSeconds: number) => void;
}

/**
 * Wraps a `@pipecat-ai/client-js` `PipecatClient` configured with
 * `@pipecat-ai/small-webrtc-transport` pointed at `POST /api/offer`
 * (CLNT-02). This is "the one connect path": Bearer token -> `/api/offer` ->
 * SmallWebRTC negotiation -> RTVI events.
 *
 * CRITICAL fix for a real vendor-library gap (T-05-04-E): `SmallWebRTCTransport`
 * 1.10.5's `negotiate()` catches ANY error from its `/api/offer` POST --
 * including a 401/403/429 JSON error body -- and silently schedules a retry
 * via its own internal `attemptReconnection()` rather than rejecting; after
 * `maxReconnectionAttempts` (3) it just calls `stop()` with no reject either.
 * `PipecatClient.connect()`'s returned promise therefore NEVER settles on an
 * auth/quota reject, and there is no public callback hook for the raw HTTP
 * response. Left unfixed, the app would show an infinite "Connecting…"
 * spinner on every reject (the server never answers with a real SDP either
 * way, so no media starts -- but the *client* would never know to stop
 * waiting). We fix this with a short-lived `window.fetch` interceptor scoped
 * to exactly one `connect()` call: it inspects (never mutates) the response
 * for the `/api/offer` POST and, on a non-2xx, immediately dispatches a
 * typed `OFFER_REJECTED` event with the real status + JSON error body, then
 * proactively disconnects the client so it stops silently retrying against a
 * gate that will keep saying no. The interceptor is always restored.
 *
 * Known residual limitation (flagged for 05-06, which owns retry/backoff
 * policy): because the vendor transport's retry scheduling isn't fully
 * cancelable through its public API, a already-in-flight `setTimeout`-based
 * reconnection attempt scheduled by `negotiate()`'s own catch block before
 * our `disconnect()` call lands could still fire once in the background
 * after we've already surfaced "rejected" to the UI. This causes at most a
 * few extra (cheap, no-media) auth/gate checks server-side within ~6s, not a
 * security or spend concern (the server's start_gate remains authoritative
 * either way) -- but 05-06 should confirm this live and consider hardening
 * further if it proves user-visible.
 */
function readSessionMaxSeconds(body: unknown): number | null {
  const value = (body as { session_max_seconds?: unknown } | null)?.session_max_seconds;
  return typeof value === "number" ? value : null;
}

export function createVoiceSession(options: CreateVoiceSessionOptions): VoiceSession {
  const { getToken, onEvent, rtvi, onSessionMax } = options;

  const transport = new SmallWebRTCTransport({
    webrtcRequestParams: buildConnectParams(getToken()),
  });

  // Bot audio PLAYBACK. SmallWebRTCTransport surfaces the concierge's incoming
  // audio track via `onTrackStarted` but never plays it — the app must attach
  // it to an <audio> element itself. Without this the entire pipeline runs
  // (mic → STT → LLM → TTS, captions + latency HUD all update) yet the user
  // hears nothing. The element is never added to the DOM (headless playback is
  // fine) and its lifetime is tied to this session. `.play()` is allowed
  // because the session only ever starts from the "Tap to talk" user gesture
  // (which also unlocks iOS audio).
  const botAudioEl =
    typeof document !== "undefined" ? document.createElement("audio") : null;
  if (botAudioEl) botAudioEl.autoplay = true;

  const client = new PipecatClient({
    transport,
    enableMic: true,
    enableCam: false,
    callbacks: {
      ...rtvi,
      onTrackStarted: (track, participant) => {
        // Play ONLY the bot's incoming audio. SmallWebRTCTransport fires this
        // callback for BOTH the remote bot track (no participant) AND the
        // LOCAL mic track (participant.local === true) — playing the latter
        // back is exactly what made the user hear their own voice echoed.
        if (
          botAudioEl &&
          track.kind === "audio" &&
          participant?.local !== true
        ) {
          botAudioEl.srcObject = new MediaStream([track]);
          void botAudioEl.play().catch(() => {
            /* autoplay blocked (should not happen post-gesture) — non-fatal */
          });
        }
        rtvi?.onTrackStarted?.(track, participant);
      },
      onBotReady: (data) => {
        onEvent({ type: "CONNECTED" });
        rtvi?.onBotReady?.(data);
      },
      onDisconnected: () => {
        onEvent({ type: "DISCONNECTED" });
        rtvi?.onDisconnected?.();
      },
      onTransportStateChanged: (state) => {
        if (state === "error") onEvent({ type: "TRANSPORT_ERROR" });
        rtvi?.onTransportStateChanged?.(state);
      },
      onError: (message) => {
        const data = message.data as { message?: string } | undefined;
        onEvent({ type: "TRANSPORT_ERROR", message: data?.message });
        rtvi?.onError?.(message);
      },
      onBotStartedSpeaking: () => {
        // Half-duplex echo guard (esp. mobile speakerphone). While the bot is
        // speaking, mute the mic so its loudspeaker output can't loop back into
        // the mic and make the agent transcribe + reply to its OWN voice
        // (observed on Apple devices on speaker). Trades barge-in for a clean,
        // self-loop-free conversation — the right default for a phone on
        // speaker or a noisy conference floor. `client` is assigned by the time
        // this runtime callback fires.
        client.enableMic(false);
        rtvi?.onBotStartedSpeaking?.();
      },
      onBotStoppedSpeaking: () => {
        client.enableMic(true);
        rtvi?.onBotStoppedSpeaking?.();
      },
    },
  });

  async function connect(): Promise<void> {
    return new Promise((resolve) => {
      let settled = false;
      const originalFetch = window.fetch.bind(window);

      window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
        const response = await originalFetch(input, init);
        if (isOfferRequest(input, init)) {
          if (!settled && !response.ok) {
            settled = true;
            window.fetch = originalFetch;
            const rejection = await parseRejection(response);
            onEvent({ type: "OFFER_REJECTED", rejection });
            // Stop the vendor client's silent retry loop -- it doesn't know
            // the reject is final (see docstring above).
            void client.disconnect().catch(() => {
              /* already tearing down; nothing to recover */
            });
            resolve();
          } else if (response.ok && onSessionMax) {
            // Non-blocking peek at the answer body for the CLNT-05 countdown
            // cap (see `onSessionMax` docstring) -- never consumes/mutates
            // the response the vendor transport still needs to read.
            void response
              .clone()
              .json()
              .then((body: unknown) => {
                const sessionMax = readSessionMaxSeconds(body);
                if (sessionMax != null) onSessionMax(sessionMax);
              })
              .catch(() => {
                /* non-JSON/odd body -- countdown simply won't render, not fatal */
              });
          }
        }
        return response;
      };

      client
        .connect()
        .then(() => {
          if (settled) return;
          settled = true;
          window.fetch = originalFetch;
          resolve();
        })
        .catch((err: unknown) => {
          if (settled) return;
          settled = true;
          window.fetch = originalFetch;
          onEvent({
            type: "TRANSPORT_ERROR",
            message: err instanceof Error ? err.message : undefined,
          });
          resolve();
        });
    });
  }

  async function disconnect(): Promise<void> {
    await client.disconnect();
    if (botAudioEl) {
      botAudioEl.pause();
      botAudioEl.srcObject = null;
    }
  }

  return { client, connect, disconnect };
}
