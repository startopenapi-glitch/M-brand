import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App";
try {
  createRoot(document.getElementById("root")!).render(
    <StrictMode><App /></StrictMode>
  );
  (window as any).__APP_READY__ = true;
} catch (error) {
  (window as any).__APP_READY__ = false;
  throw error;
}
