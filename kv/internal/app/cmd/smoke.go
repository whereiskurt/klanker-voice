package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"text/tabwriter"
	"time"

	"github.com/pion/webrtc/v4"
	"github.com/spf13/cobra"
)

// defaultSmokeEndpoint is voice.klankermaker.ai — the deployed public-IP
// Fargate task's HTTPS signaling endpoint. Overridable via --endpoint for
// staging.
const defaultSmokeEndpoint = "https://voice.klankermaker.ai"

// smokeTokenEnvVar names the dedicated smoke/service credential
// (KMV_SMOKE_SERVICE_TOKEN, D-15) that apps/voice/src/klanker_voice/auth.py's
// recognize_service_credential checks first, bypassing JWKS validation and
// marking the session bypass_accounting=True. Never printed by kv.
const smokeTokenEnvVar = "KMV_SMOKE_SERVICE_TOKEN"

// smokeICETimeout bounds how long we wait for ICEConnectionState to reach
// connected/completed before declaring FAIL (T-04-16: a stuck smoke session
// must never hang or hold a slot indefinitely).
const smokeICETimeout = 15 * time.Second

// smokeRTPWindow is how long, once ICE connects, we listen for inbound RTP
// packets before counting them — the server's greet-first pipeline starts
// speaking as soon as the session begins, so a short window is enough to
// observe real media flow (D-15: media must actually flow, not just ICE
// connect).
const smokeRTPWindow = 5 * time.Second

// smokeHTTPTimeout bounds the /api/offer signaling POST itself.
const smokeHTTPTimeout = 15 * time.Second

// smokeOfferRequest mirrors apps/voice/server.py's SmallWebRTCRequest
// request shape (sdp/type) — kv is a non-browser client of /api/offer so it
// must match that contract exactly, not the pipecat client-js shape.
type smokeOfferRequest struct {
	SDP  string `json:"sdp"`
	Type string `json:"type"`
}

// smokeOfferResponse mirrors the /api/offer success shape
// ({"sdp","type","pc_id"}) and its typed-rejection error shape
// ({"error","detail"}) from server.py.
type smokeOfferResponse struct {
	SDP    string `json:"sdp"`
	Type   string `json:"type"`
	PCID   string `json:"pc_id"`
	Error  string `json:"error"`
	Detail string `json:"detail"`
}

// SmokeResult is the outcome of one `kv smoke` run — the pass/fail facts an
// operator (or the Task-3 deploy checkpoint) needs to confirm INFR-03/D-15:
// offer -> ICE connected -> RTP frames actually flowing.
type SmokeResult struct {
	Endpoint       string   `json:"endpoint"`
	Pass           bool     `json:"pass"`
	ICEState       string   `json:"iceState"`
	CandidateTypes []string `json:"candidateTypes"`
	RTPPackets     int      `json:"rtpPackets"`
	Detail         string   `json:"detail,omitempty"`
}

// NewSmokeCmd builds the "kv smoke" command (KV-05, D-15): sends a real
// synthetic WebRTC offer to the live /api/offer, negotiates ICE to
// connected, and asserts inbound RTP frames actually flow before tearing
// down — the deployed proof that INFR-03's real browser<->task UDP media
// path works, not just that the port is reachable.
func NewSmokeCmd(cfg *Config) *cobra.Command {
	var (
		endpoint string
		asJSON   bool
	)

	smokeCmd := &cobra.Command{
		Use:   "smoke",
		Short: "Prove offer -> ICE connected -> RTP-flow against the voice service (KV-05)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			token := os.Getenv(smokeTokenEnvVar)
			if token == "" {
				return fmt.Errorf("%s is not set (the smoke/service credential is required)", smokeTokenEnvVar)
			}
			result, err := runSmoke(c.Context(), endpoint, token)
			if err != nil {
				return err
			}
			return printSmokeResult(c, result, asJSON)
		},
	}
	smokeCmd.Flags().StringVar(&endpoint, "endpoint", defaultSmokeEndpoint, "voice service base URL")
	smokeCmd.Flags().BoolVar(&asJSON, "json", false, "output as JSON")
	return smokeCmd
}

// runSmoke drives the full offer -> ICE -> RTP-flow cycle once and always
// closes the PeerConnection before returning (T-04-16). The returned error
// is reserved for setup/transport failures the operator must fix (bad
// endpoint, network error); a reachable-but-failing smoke run (auth reject,
// ICE never connects, no RTP observed) is reported via SmokeResult.Pass so
// the candidate/ICE-state detail is still visible to the caller.
func runSmoke(ctx context.Context, endpoint, token string) (SmokeResult, error) {
	result := SmokeResult{Endpoint: endpoint, ICEState: webrtc.ICEConnectionStateNew.String()}

	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{{URLs: []string{"stun:stun.l.google.com:19302"}}},
	})
	if err != nil {
		return result, fmt.Errorf("create peer connection: %w", err)
	}
	defer pc.Close() //nolint:errcheck

	if _, err := pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		return result, fmt.Errorf("add audio transceiver: %w", err)
	}

	var candidateTypes []string
	pc.OnICECandidate(func(candidate *webrtc.ICECandidate) {
		if candidate != nil {
			candidateTypes = append(candidateTypes, candidate.Typ.String())
		}
	})

	iceStateCh := make(chan webrtc.ICEConnectionState, 16)
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		iceStateCh <- state
	})

	var rtpPackets int64
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			if _, _, err := track.ReadRTP(); err != nil {
				return
			}
			atomic.AddInt64(&rtpPackets, 1)
		}
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return result, fmt.Errorf("create offer: %w", err)
	}

	gatherComplete := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return result, fmt.Errorf("set local description: %w", err)
	}
	select {
	case <-gatherComplete:
	case <-time.After(smokeICETimeout):
		return result, fmt.Errorf("timed out gathering local ICE candidates")
	}

	answer, err := postOffer(ctx, endpoint, token, pc.LocalDescription().SDP)
	if err != nil {
		return result, err
	}
	if answer.Error != "" {
		result.Detail = fmt.Sprintf("/api/offer rejected: %s %s", answer.Error, answer.Detail)
		return result, nil
	}
	if answer.SDP == "" {
		result.Detail = "/api/offer returned no SDP answer"
		return result, nil
	}

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answer.SDP,
	}); err != nil {
		return result, fmt.Errorf("set remote description: %w", err)
	}

	if !waitForICEConnected(iceStateCh, smokeICETimeout, &result) {
		result.CandidateTypes = dedupeStrings(candidateTypes)
		return result, nil
	}

	// ICE is connected; give the server's greet-first TTS a short window to
	// actually flow audio before we count packets and tear down.
	time.Sleep(smokeRTPWindow)

	result.CandidateTypes = dedupeStrings(candidateTypes)
	result.RTPPackets = int(atomic.LoadInt64(&rtpPackets))
	result.Pass = result.RTPPackets > 0
	if !result.Pass {
		result.Detail = "ICE connected but no inbound RTP packets observed within the window"
	}
	return result, nil
}

// waitForICEConnected blocks on iceStateCh until a connected/completed state
// arrives (returns true, updating result.ICEState), a terminal failure state
// arrives, or timeout elapses (both return false with result.Detail set).
func waitForICEConnected(iceStateCh <-chan webrtc.ICEConnectionState, timeout time.Duration, result *SmokeResult) bool {
	deadline := time.After(timeout)
	for {
		select {
		case state := <-iceStateCh:
			result.ICEState = state.String()
			switch state {
			case webrtc.ICEConnectionStateConnected, webrtc.ICEConnectionStateCompleted:
				return true
			case webrtc.ICEConnectionStateFailed, webrtc.ICEConnectionStateClosed, webrtc.ICEConnectionStateDisconnected:
				result.Detail = fmt.Sprintf("ICE entered %s before connecting", state)
				return false
			}
		case <-deadline:
			result.Detail = fmt.Sprintf("timed out waiting for ICE connected (last state: %s)", result.ICEState)
			return false
		}
	}
}

// postOffer POSTs the synthetic offer to <endpoint>/api/offer with the
// smoke/service credential in the Authorization header, in the exact body
// shape server.py's SmallWebRTCRequest.from_dict expects.
func postOffer(ctx context.Context, endpoint, token, sdp string) (smokeOfferResponse, error) {
	var out smokeOfferResponse

	body, err := json.Marshal(smokeOfferRequest{SDP: sdp, Type: "offer"})
	if err != nil {
		return out, fmt.Errorf("marshal offer request: %w", err)
	}

	url := strings.TrimRight(endpoint, "/") + "/api/offer"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("build /api/offer request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+token)

	httpClient := &http.Client{Timeout: smokeHTTPTimeout}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return out, fmt.Errorf("POST /api/offer: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("decode /api/offer response (status %d): %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusOK && out.Error == "" {
		out.Error = fmt.Sprintf("http-%d", resp.StatusCode)
	}
	return out, nil
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func printSmokeResult(c *cobra.Command, result SmokeResult, asJSON bool) error {
	out := c.OutOrStdout()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else {
		status := "FAIL"
		if result.Pass {
			status = "PASS"
		}
		w := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
		fmt.Fprintf(w, "STATUS\t%s\n", status)
		fmt.Fprintf(w, "ENDPOINT\t%s\n", result.Endpoint)
		fmt.Fprintf(w, "ICE-STATE\t%s\n", result.ICEState)
		fmt.Fprintf(w, "CANDIDATES\t%s\n", strings.Join(result.CandidateTypes, ","))
		fmt.Fprintf(w, "RTP-PACKETS\t%d\n", result.RTPPackets)
		if result.Detail != "" {
			fmt.Fprintf(w, "DETAIL\t%s\n", result.Detail)
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}
	if !result.Pass {
		return fmt.Errorf("smoke FAILED")
	}
	return nil
}
