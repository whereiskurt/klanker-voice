import Attract from "./screens/Attract";

// klanker-voice — App shell.
//
// Renders the full-bleed 100dvh immersive stage background (D-05) with the
// D-07 attract landing as the initial screen. onTapToTalk is a stub seam —
// 05-03 routes it to the OIDC authorization-code+PKCE sign-in redirect.
export default function App() {
  const handleTapToTalk = () => {
    console.info("klanker-voice: tap-to-talk — sign-in wiring lands in 05-03");
  };

  return (
    <div className="stage">
      <Attract onTapToTalk={handleTapToTalk} />
    </div>
  );
}
