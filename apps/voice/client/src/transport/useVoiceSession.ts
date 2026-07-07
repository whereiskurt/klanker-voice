import { useCallback, useEffect, useRef, useState } from "react";
import type { PipecatClient } from "@pipecat-ai/client-js";
import { requestMic, type MicError } from "../media/getMic";
import { getToken } from "../auth/tokenStore";
import { playRandomGreeting, type GreetingHandle } from "../greeting/greetingPlayer";
import {
  connectionReducer,
  INITIAL_CONNECTION_OUTCOME,
  type ConnectionEvent,
  type ConnectionOutcome,
} from "./connectionState";
import { createVoiceSession, type VoiceSession } from "./voiceSession";
import { createRetryController, IDLE_RETRY_STATUS, type RetryController, type RetryStatus } from "./retryPolicy";

/** A clean or provider-error end of a session that had actually reached
 * "connected" (CLNT-07, D-14) -- distinct from a pre-connect "rejected"
 * (Task 2's gate) or "failed" (Task 1's retry/wall), both of which never
 * had a live conversation to summarize. */
export interface SessionSummary {
  /** Elapsed seconds from "connected" to this end, clamped >= 0. */
  elapsedSeconds: number;
  /** "clean": a normal server-driven end (goodbye/idle/timer) -- the
   * transport reported a plain disconnect, not an error.
   * "provider-error": the transport reported an error/close *after* the
   * session had already reached "connected" -- shown with the generic
   * provider-error end copy (SessionEnd.tsx) rather than routed into Task
   * 1's pre-connect retry/wall flow (which would misleadingly imply the
   * session never connected in the first place). */
  reason: "clean" | "provider-error";
}

export interface UseVoiceSessionResult {
  outcome: ConnectionOutcome;
  /** Set when `start()` stopped at the mic-permission step (Task 1). */
  micError: MicError | null;
  /** The live RTVI client once connecting has begun -- Task 3 subscribes
   * orb/caption bindings to this via `client.on(RTVIEvent.X, handler)`. */
  client: PipecatClient | null;
  /** The tier session_max_seconds carried back on the `/api/offer` answer
   * (CLNT-05, D-10); `null` until it arrives (near-immediate -- same
   * response the pipeline connects from) or if this connect never reached
   * that point. */
  sessionMaxSeconds: number | null;
  /** Bounded auto-retry status (CLNT-02, D-11) -- drives `ConnectingRetry`
   * / `UdpBlockedWall` when `outcome.state === "failed"`. */
  retryStatus: RetryStatus;
  /** Set once a live ("connected") session ends -- drives `SessionEnd.tsx`
   * (CLNT-07, D-14). Cleared as soon as a new `start()` begins. */
  sessionSummary: SessionSummary | null;
  /** Gesture-gated: call this from the "Tap to talk" handler, never on mount.
   * Also the full reconnect flow (CLNT-07): re-requests mic (a no-op prompt
   * once already granted) and issues a fresh `/api/offer`, re-running the
   * server's quota start_gate -- never a silent transport reopen. */
  start: () => Promise<void>;
  stop: () => Promise<void>;
  /** Manual "Try again" on the exhausted `UdpBlockedWall` (D-11): resets the
   * bounded retry schedule and re-attempts immediately. */
  retryNow: () => void;
  /** Dismisses a "rejected" (Task 2 gate) outcome back to idle/attract --
   * used by `GateCard`'s non-retryable actions (sign-out / dismiss). */
  dismissGate: () => void;
  /** Clears an inline mic-error message back to plain attract, WITHOUT
   * re-requesting the mic (unlike `MicError`'s own "Try again" button,
   * which calls `start()`). Wired to `Esc` in `App.tsx` (05-07 hardening:
   * UI-SPEC a11y baseline "Esc dismisses transient gate copy"). */
  dismissMicError: () => void;
}

/**
 * The live-connect React hook (CLNT-01/02/05/07): on `start()`, requests the
 * mic (Task 1's honest, distinct error states), and on success builds +
 * connects a `voiceSession` (Bearer token -> `/api/offer` -> SmallWebRTC,
 * Task 2). `connectionState` only ever reaches "connected" after a real
 * bot-ready signal -- no conversation UI can mount before that (T-05-04-E).
 *
 * 05-06 adds: bounded auto-retry + backoff on a pre-connect transport
 * failure (D-11, `retryPolicy.ts`), routing a POST-connect drop to a
 * `sessionSummary` instead (CLNT-07, D-14) so it never gets mistaken for a
 * "never connected" wall, and a `dismissGate`/`retryNow` surface for the
 * gate-card and UDP-wall CTAs.
 */
export function useVoiceSession(): UseVoiceSessionResult {
  const [outcome, setOutcome] = useState<ConnectionOutcome>(INITIAL_CONNECTION_OUTCOME);
  const [micError, setMicError] = useState<MicError | null>(null);
  const [client, setClient] = useState<PipecatClient | null>(null);
  const [sessionMaxSeconds, setSessionMaxSeconds] = useState<number | null>(null);
  const [retryStatus, setRetryStatus] = useState<RetryStatus>(IDLE_RETRY_STATUS);
  const [sessionSummary, setSessionSummary] = useState<SessionSummary | null>(null);

  const sessionRef = useRef<VoiceSession | null>(null);
  /** Tracks whether the CURRENT session ever reached "connected" -- the
   * signal that decides whether a later TRANSPORT_ERROR is a pre-connect
   * failure (-> retry/wall) or a post-connect drop (-> session summary). */
  const wasConnectedRef = useRef(false);
  /** Wall-clock start of the current "connected" period, for the
   * session-end "{m:ss} spoken" summary (CLNT-07). */
  const connectedAtRef = useRef<number | null>(null);
  /** Latched once the current attempt has been terminally rejected by the
   * server start-gate (`OFFER_REJECTED` -- a quota/auth 401/403/429, see
   * connectionState.ts). A quota rejection is TERMINAL: after it, the vendor
   * transport is proactively disconnected (voiceSession.ts), and that
   * teardown -- or a reconnection its `negotiate()` catch block already
   * scheduled -- can emit a stray `TRANSPORT_ERROR`/`DISCONNECTED`. Those
   * must NOT (a) stomp the clear "rejected" gate outcome to "failed", nor (b)
   * feed the transport retry controller, which exists ONLY for genuine
   * pre-connect ICE/transport failures (D-11). Reset by `start()`/`stop()`. */
  const rejectedRef = useRef(false);
  /** The currently-playing pre-rendered greeting clip (B-05), if any. */
  const greetingRef = useRef<GreetingHandle | null>(null);
  /** Resolves when the current greeting clip has ended (or immediately if
   * there is no clip this session) -- the CONNECTED branch below waits on
   * this so the visible Live handoff never overlaps the greeting audio. */
  const greetingEndedRef = useRef<Promise<void>>(Promise.resolve());

  const dispatch = useCallback((event: ConnectionEvent) => {
    setOutcome((current) => connectionReducer(current, event));
  }, []);

  // `handleSessionEvent` (below) closes over `dispatch` (stable) plus a few
  // refs, so it never needs to change identity across renders; it's built
  // once and stored in a ref purely so `beginConnect`/the retry controller
  // (both constructed once, also via refs) can call the LATEST version
  // without a construction-order cycle.
  const handleSessionEventRef = useRef<(event: ConnectionEvent) => void>(() => {});

  const handleSessionEvent = useCallback(
    (event: ConnectionEvent) => {
      if (event.type === "CONNECTED") {
        wasConnectedRef.current = true;
        connectedAtRef.current = Date.now();
        setSessionSummary(null);
        retryControllerRef.current?.reportSuccess();
        setRetryStatus(IDLE_RETRY_STATUS);
        // Hold the visible "connected/Live" state until the greeting clip has
        // finished -- greeting audio (speaker) and live STT (mic) must not overlap.
        void greetingEndedRef.current.then(() => dispatch(event));
        return;
      }

      if (event.type === "OFFER_REJECTED") {
        // A terminal server start-gate reject (quota/auth). Latch it so any
        // late transport-teardown noise from voiceSession.ts's proactive
        // client.disconnect() (or the vendor's own still-scheduled
        // reconnection) can't convert this clear gate into a "failed"
        // retry/wall below. Dispatch immediately -- unlike CONNECTED, a
        // rejection is NOT held behind the greeting handoff, so the GateCard
        // shows right away even while the greeting clip is still playing.
        rejectedRef.current = true;
        dispatch(event);
        return;
      }

      if (event.type === "DISCONNECTED" || event.type === "TRANSPORT_ERROR") {
        if (rejectedRef.current) {
          // This attempt was already terminally rejected by the start-gate --
          // swallow the trailing transport noise entirely: no "failed" stomp,
          // and crucially no retryController.reportFailure() (retry is for
          // pre-connect ICE/transport failures only, never a quota reject).
          return;
        }

        if (wasConnectedRef.current) {
          // A drop AFTER a live session -- CLNT-07 session-end summary, not
          // Task 1's pre-connect retry/wall flow.
          const elapsedSeconds =
            connectedAtRef.current != null ? Math.max(0, (Date.now() - connectedAtRef.current) / 1000) : 0;
          wasConnectedRef.current = false;
          connectedAtRef.current = null;
          setSessionSummary({
            elapsedSeconds,
            reason: event.type === "TRANSPORT_ERROR" ? "provider-error" : "clean",
          });
          // Land on a clean "idle" outcome either way -- SessionEnd renders
          // off `sessionSummary`, not off outcome.state.
          dispatch({ type: "RESET" });
          return;
        }

        if (event.type === "TRANSPORT_ERROR") {
          // A genuine pre-connect transport/ICE failure -- Task 1's bounded
          // retry policy owns what happens next. Halt the greeting clip so
          // it never plays over the retry UI / UdpBlockedWall (B-05).
          greetingRef.current?.stop();
          greetingRef.current = null;
          dispatch(event);
          retryControllerRef.current?.reportFailure();
          return;
        }
      }

      dispatch(event);
    },
    [dispatch],
  );

  useEffect(() => {
    handleSessionEventRef.current = handleSessionEvent;
  }, [handleSessionEvent]);

  /** Creates a fresh `VoiceSession` (a new `/api/offer` POST -- never a
   * silent transport reopen) and connects it. Shared by the very first
   * `start()` attempt and every subsequent auto/manual retry. */
  const beginConnect = useCallback((): Promise<void> => {
    const session = createVoiceSession({
      getToken,
      onEvent: (event) => handleSessionEventRef.current(event),
      onSessionMax: setSessionMaxSeconds,
    });
    sessionRef.current = session;
    setClient(session.client);
    return session.connect();
  }, []);

  const beginConnectRef = useRef(beginConnect);
  useEffect(() => {
    beginConnectRef.current = beginConnect;
  }, [beginConnect]);

  // One long-lived retry controller per hook instance (CLNT-02, D-11) --
  // lazily constructed exactly once via the standard ref-init idiom (a bare
  // `useRef(createRetryController(...))` would re-evaluate the constructor
  // argument on every render, only to discard all but the first).
  const retryControllerRef = useRef<RetryController | null>(null);
  if (retryControllerRef.current == null) {
    retryControllerRef.current = createRetryController({
      // A retry never re-requests the mic -- permission is already granted
      // from the original `start()`; only the transport/offer needs
      // re-attempting.
      attemptConnect: () => void beginConnectRef.current(),
      onRetrying: (attempt, totalAttempts) => setRetryStatus({ kind: "retrying", attempt, totalAttempts }),
      onExhausted: () => setRetryStatus({ kind: "exhausted" }),
    });
  }

  useEffect(() => {
    const controller = retryControllerRef.current;
    return () => {
      controller?.cancel();
    };
  }, []);

  const start = useCallback(async () => {
    setMicError(null);
    setSessionMaxSeconds(null);
    setSessionSummary(null);
    setRetryStatus(IDLE_RETRY_STATUS);
    retryControllerRef.current?.reset();
    wasConnectedRef.current = false;
    connectedAtRef.current = null;
    rejectedRef.current = false;
    dispatch({ type: "REQUEST_MIC" });

    const mic = await requestMic();
    if (mic.status !== "granted") {
      setMicError(mic.status);
      dispatch({ type: "MIC_ERROR" });
      return;
    }
    // `requestMic()` only PROVES permission (and, on iOS, unlocks audio
    // playback via the same gesture). It must NOT keep the device open:
    // PipecatClient (`enableMic: true`) does its OWN `getUserMedia` inside
    // `connect()`, so leaving this probe stream live means two concurrent
    // captures of the same mic. Confirmed via chrome://webrtc-internals
    // (two getUserMedia calls, two distinct track ids) — the track actually
    // attached to the peer connection then delivers no audio and the server
    // tears the session down with "No audio frame received". Releasing the
    // probe stream here leaves the client's capture as the sole live one.
    // Permission persists at the browser level, so the client's capture does
    // not re-prompt.
    mic.stream.getTracks().forEach((track) => track.stop());
    dispatch({ type: "MIC_GRANTED" });

    // Instant greeting: play a random pre-rendered clip on this same gesture
    // (unlocks iOS audio). Runs concurrently with connect; the CONNECTED
    // handoff above waits for it so the greeting never overlaps live STT.
    greetingRef.current?.stop();
    const handle = await playRandomGreeting();
    greetingRef.current = handle;
    greetingEndedRef.current = handle ? handle.ended : Promise.resolve();

    await beginConnect();
  }, [dispatch, beginConnect]);

  const stop = useCallback(async () => {
    retryControllerRef.current?.cancel();
    greetingRef.current?.stop();
    greetingRef.current = null;
    await sessionRef.current?.disconnect();
    sessionRef.current = null;
    setClient(null);
    setSessionMaxSeconds(null);
    wasConnectedRef.current = false;
    connectedAtRef.current = null;
    rejectedRef.current = false;
    dispatch({ type: "RESET" });
  }, [dispatch]);

  const retryNow = useCallback(() => {
    setRetryStatus(IDLE_RETRY_STATUS);
    dispatch({ type: "RESET" });
    retryControllerRef.current?.retryNow();
  }, [dispatch]);

  const dismissGate = useCallback(() => {
    setSessionSummary(null);
    dispatch({ type: "RESET" });
  }, [dispatch]);

  const dismissMicError = useCallback(() => {
    setMicError(null);
  }, []);

  return {
    outcome,
    micError,
    client,
    sessionMaxSeconds,
    retryStatus,
    sessionSummary,
    start,
    stop,
    retryNow,
    dismissGate,
    dismissMicError,
  };
}
