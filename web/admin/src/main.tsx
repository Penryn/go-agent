import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import "./styles.css";

const cspNonce = document.querySelector('meta[name="csp-nonce"]')?.getAttribute("content") ?? undefined;
const root = document.getElementById("root");

if (!root) {
  throw new Error("missing #root element");
}

createRoot(root).render(
  <StrictMode>
    <App cspNonce={cspNonce} />
  </StrictMode>,
);
