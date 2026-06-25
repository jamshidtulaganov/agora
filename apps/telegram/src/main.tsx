import { createRoot } from "react-dom/client";
import { App } from "./app";
import { telegramReady } from "./telegram/sdk";
import { applyTelegramTheme } from "./telegram/theme";
import "./styles.css";

// Apply the Telegram theme and signal readiness before mounting so the first
// paint already uses the user's colors and the full-height viewport.
applyTelegramTheme();
telegramReady();

const container = document.getElementById("root");
if (container) {
  // No StrictMode: it double-invokes effects in dev, which would fire the
  // initData login POST twice on first open.
  createRoot(container).render(<App />);
}
