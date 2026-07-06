import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./styles/tokens.css";
import "./styles/global.css";
import "./styles/responsive.css";

const rootEl = document.getElementById("root");
if (!rootEl) {
  throw new Error("klanker-voice: #root mount element not found in index.html");
}

createRoot(rootEl).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
